package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"github.com/oleksii-kalinin/learn-file-storage-s3-golang-starter/internal/auth"
)

type Stream struct {
	Index       int    `json:"index"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	CodecType   string `json:"codec_type"`
	AspectRatio string `json:"display_aspect_ratio"`
}

type FFprobe struct {
	Streams []Stream `json:"streams"`
}

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	const uploadLimit = 1 << 30
	const maxMemory = 10 << 20
	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT", err)
		return
	}

	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}

	log.Println("uploading video", videoID, "by user", userID)

	r.Body = http.MaxBytesReader(w, r.Body, uploadLimit)

	if err := r.ParseMultipartForm(maxMemory); err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid multipart form", err)
		return
	}

	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	videoData, fh, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Error uploading video", err)
		return
	}
	defer videoData.Close()

	tempVideo, err := os.CreateTemp("", "tubely-upload.mp4")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "error temping the video", err)
		return
	}
	defer os.Remove(tempVideo.Name())
	defer tempVideo.Close()

	_, err = io.Copy(tempVideo, videoData)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "error copying video", err)
		return
	}
	_, _ = tempVideo.Seek(0, io.SeekStart)

	ar, err := getVideoAspectRatio(tempVideo.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "error checking for aspect ration, defaulting to other", err)
		return
	}

	var aspect string
	switch ar {
	case "16:9":
		aspect = "landscape"
	case "9:16":
		aspect = "portrait"
	default:
		aspect = "other"
	}

	log.Printf("Video: %s, ratio: %s", videoID, aspect)

	videoMetaData, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error getting video", err)
		return
	}
	if videoMetaData.ID == uuid.Nil {
		respondWithError(w, http.StatusNotFound, "video not found", nil)
		return
	}

	if userID != videoMetaData.UserID {
		respondWithError(w, http.StatusForbidden, "Denied", nil)
		return
	}

	videoMediaType := fh.Header.Get("Content-Type")
	// Validate and extract extension
	var ext string
	switch videoMediaType {
	case "video/mp4":
		ext = "mp4"
	default:
		respondWithError(w, http.StatusBadRequest, "Invalid Content-Type. Allowed: video/mp4", nil)
		return
	}

	videoRandomBase := make([]byte, 32)
	if _, err = rand.Read(videoRandomBase); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error generating video filename", err)
		return
	}

	videoFileName := fmt.Sprintf("%s/%s.%s", aspect, base64.RawURLEncoding.EncodeToString(videoRandomBase), ext)

	uploadCtx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	_, err = cfg.s3Client.PutObject(uploadCtx, &s3.PutObjectInput{
		Bucket:             &cfg.s3Bucket,
		Key:                &videoFileName,
		Body:               tempVideo,
		ContentType:        &videoMediaType,
		ContentLength:      &fh.Size,
		ContentDisposition: aws.String("inline"),
		CacheControl:       aws.String("public, max-age=31536000, immutable"),
		ACL:                "public-read",
	})
	if err != nil {
		log.Println(err)
		respondWithError(w, http.StatusInternalServerError, "error upload to S3", err)
		return
	}

	videoURL := fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", cfg.s3Bucket, cfg.s3Region, videoFileName)
	videoMetaData.VideoURL = &videoURL
	err = cfg.db.UpdateVideo(videoMetaData)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error updating DB", err)
		return
	}

	respondWithJSON(w, http.StatusOK, videoMetaData)
}

func getVideoAspectRatio(filePath string) (string, error) {
	buf := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}
	var err error

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-print_format", "json", "-show_streams", filePath)
	cmd.Stdout = buf
	cmd.Stderr = errBuf

	err = cmd.Run()
	if err != nil {
		return "", fmt.Errorf("ffprobe failed: %w, srderr: %s", err, errBuf.String())
	}

	var ff FFprobe
	err = json.Unmarshal(buf.Bytes(), &ff)
	if err != nil {
		return "", err
	}

	var w, h int
	for _, s := range ff.Streams {
		if s.CodecType == "video" {
			w, h = s.Width, s.Height
			break
		}
	}
	if w == 0 || h == 0 {
		return "", errors.New("no video stream found")
	}
	ratio := float64(w) / float64(h)

	if math.Abs(ratio-16.0/9.0) < 0.01 {
		return "16:9", nil
	}
	if math.Abs(ratio-9.0/16.0) < 0.01 {
		return "9:16", nil
	}
	return "other", nil
}
