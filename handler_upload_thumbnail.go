package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

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
		respondWithError(w, http.StatusInternalServerError, "Invalid multipart form", err)
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
		respondWithError(w, http.StatusInternalServerError, "Error getting video", err)
		return
	}

	if userID != videoMetaData.UserID {
		respondWithError(w, http.StatusForbidden, "Denied", err)
		return
	}

	thumbMediaType := fh.Header.Get("Content-Type")

	ext := strings.Split(thumbMediaType, "/")[1]
	thumbFilePath := filepath.Join(cfg.assetsRoot, fmt.Sprintf("%s.%s", videoIDString, ext))

	thumbnailURL := fmt.Sprintf("http://localhost:%s/assets/%s.%s", cfg.port, videoIDString, ext)

	thumbnail, err := os.Create(thumbFilePath)

	io.Copy(thumbnail, thumbnailData)
	defer thumbnail.Close()

	videoMetaData.ThumbnailURL = &thumbnailURL
	err = cfg.db.UpdateVideo(videoMetaData)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error updating DB", err)
		return
	}

	respondWithJSON(w, http.StatusOK, videoMetaData)
}
