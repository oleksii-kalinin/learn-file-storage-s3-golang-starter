package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadThumbnail(w http.ResponseWriter, r *http.Request) {
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
	fmt.Println("uploading thumbnail for video", videoID, "by user", userID)

	// TODO: implement the upload here

	const maxMemory = 10 << 20
	const maxUploadBytes = 10 << 20
	// const assetsPath = "/assets/"

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)

	if err := r.ParseMultipartForm(maxMemory); err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid multipart form", err)
		return
	}

	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	thumbnailData, fh, err := r.FormFile("thumbnail")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Error uploading thumbnail", err)
		return
	}
	defer thumbnailData.Close()

	videoMetaData, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error getting video", nil)
		return
	}

	if userID != videoMetaData.UserID {
		respondWithError(w, http.StatusForbidden, "Denied", err)
		return
	}

	thumbMediaType := fh.Header.Get("Content-Type")
	// Validate and extract extension
	var ext string
	switch thumbMediaType {
	case "image/jpeg":
		ext = "jpeg"
	case "image/jpg":
		ext = "jpg"
	case "image/png":
		ext = "png"
	default:
		respondWithError(w, http.StatusBadRequest, "Invalid Content-Type. Allowed: image/jpeg, image/png", nil)
		return
	}

	thumbnailRandomBase := make([]byte, 32)
	if _, err = rand.Read(thumbnailRandomBase); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error generation thumbnail filename", err)
		return
	}

	thumbFileName := fmt.Sprintf("%s.%s", base64.RawURLEncoding.EncodeToString(thumbnailRandomBase), ext)

	thumbFilePath := filepath.Join(cfg.assetsRoot, thumbFileName)
	thumbnailURL := fmt.Sprintf("http://%s:%s/assets/%s", cfg.baseURL, cfg.port, thumbFileName)

	thumbnail, err := os.Create(thumbFilePath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error creating thumbnail file", err)
		return
	}
	defer thumbnail.Close()

	_, err = io.Copy(thumbnail, thumbnailData)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error copying thumbnail file", err)
		return
	}

	videoMetaData.ThumbnailURL = &thumbnailURL
	err = cfg.db.UpdateVideo(videoMetaData)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error updating DB", err)
		return
	}

	respondWithJSON(w, http.StatusOK, videoMetaData)
}
