package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log"
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"github.com/oleksii-kalinin/learn-file-storage-s3-golang-starter/internal/auth"
)

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
		respondWithError(w, http.StatusInternalServerError, "Error generation video filename", err)
		return
	}

	videoFileName := fmt.Sprintf("%s.%s", base64.RawURLEncoding.EncodeToString(videoRandomBase), ext)

	_, err = cfg.s3Client.PutObject(r.Context(), &s3.PutObjectInput{
		Bucket:             &cfg.s3Bucket,
		Key:                &videoFileName,
		Body:               videoData,
		ContentType:        &videoMediaType,
		ContentLength:      &fh.Size,
		ContentDisposition: aws.String("inline"),
		CacheControl:       aws.String("public, max-age=31536000, immutable"),
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
