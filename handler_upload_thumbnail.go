package main

import (
	"fmt"
	"io"
	"net/http"

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

	r.ParseMultipartForm(maxMemory)

	thumbnailData, _, err := r.FormFile("thumbnail")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error upploading thumbnail", err)
		return
	}

	thumbnailFile, err := io.ReadAll(thumbnailData)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error working with thumbnail", err)
		return
	}

	videoMetaData, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error getting video", err)
		return
	}

	if userID != videoMetaData.UserID {
		respondWithError(w, http.StatusUnauthorized, "Denied", err)
		return
	}

	thumb := thumbnail{
		data:      thumbnailFile,
		mediaType: r.Header.Get("Content-Type"),
	}

	videoThumbnails[videoID] = thumb

	thumbURL := fmt.Sprintf("/api/thumbnails/%s", videoID)
	videoMetaData.ThumbnailURL = &thumbURL
	err = cfg.db.UpdateVideo(videoMetaData)
	if userID != videoMetaData.UserID {
		respondWithError(w, http.StatusInternalServerError, "Unable to update thumbnail in DB", err)
		return
	}
	respondWithJSON(w, http.StatusOK, videoMetaData)
}
