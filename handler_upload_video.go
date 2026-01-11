package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"github.com/oleksii-kalinin/learn-file-storage-s3-golang-starter/internal/auth"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	const uploadLimit = 1 << 30
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

	videoData, fh, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Error uploading thumbnail", err)
		return
	}
	defer videoData.Close()

	videoMetaData, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error getting video", nil)
		return
	}

	if userID != videoMetaData.UserID {
		respondWithError(w, http.StatusForbidden, "Denied", err)
		return
	}

	videoMediaType := fh.Header.Get("Content-Type")
	// Validate and extract extension
	var ext string
	switch videoMediaType {
	case "video/mp4":
		ext = "mp4"
	case "video/mkv":
		ext = "mkv"
	default:
		respondWithError(w, http.StatusBadRequest, "Invalid Content-Type. Allowed: video/mp4, video/mkv", nil)
		return
	}

	tempVideo, err := os.CreateTemp("", "tubely-upload.mp4")
	defer os.Remove(tempVideo.Name())
	defer tempVideo.Close()
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "error creating temp file", err)
		return
	}

	_, err = io.Copy(tempVideo, videoData)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "error copying video to temp file", err)
		return
	}

	_, _ = tempVideo.Seek(0, io.SeekStart)

	videoRandomBase := make([]byte, 32)
	if _, err = rand.Read(videoRandomBase); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error generation thumbnail filename", err)
		return
	}

	videoFileName := fmt.Sprintf("%s.%s", base64.RawURLEncoding.EncodeToString(videoRandomBase), ext)

	_, err = cfg.s3Client.PutObject(r.Context(), &s3.PutObjectInput{
		Bucket:      &cfg.s3Bucket,
		Key:         &videoFileName,
		Body:        tempVideo,
		ContentType: &videoMediaType,
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
