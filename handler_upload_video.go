package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

const maxVideoSize = 1 << 30

type AspectRatio string

const (
	AspectRatioOther AspectRatio = "other"
	AspectRatio16_9              = "landscape"
	AspectRatio9_16              = "portrait"
)

type ffStream struct {
	Width  int
	Height int
}

type ffStreams struct {
	Streams []ffStream
}

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
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

	metadata, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to prepare database for video", err)
		return
	}

	if userID != metadata.UserID {
		respondWithError(w, http.StatusUnauthorized, "User does not own the video", nil)
		return
	}

	fmt.Println("uploading video", videoID, "by user", userID)

	r.Body = http.MaxBytesReader(w, r.Body, maxVideoSize)
	file, header, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to parse form file", err)
		return
	}
	defer file.Close()

	mediatype, _, err := mime.ParseMediaType(header.Header.Get("Content-Type"))
	if err != nil || mediatype != "video/mp4" {
		respondWithError(w, http.StatusBadRequest, "Media Content-Type is not 'video/mp4' but '"+mediatype+"'", err)
		return
	}

	tmpFile, err := os.CreateTemp("", "tubely-upload*.mp4")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to save video file", err)
		return
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	_, err = io.Copy(tmpFile, file)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to save video file", err)
		return
	}

	aspectRatio, err := getVideoAspectRatio(tmpFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to get video aspect ratio", err)
		return
	}

	processedFilePath, err := processVideoForFastStart(tmpFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to process video file for fast start", err)
		return
	}

	processed, err := os.Open(processedFilePath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to open processed video file", err)
		return
	}
	defer os.Remove(processed.Name())
	defer processed.Close()

	s3Key := string(aspectRatio) + "/" + videoIDString + ".mp4"

	_, err = cfg.s3Client.PutObject(r.Context(), &s3.PutObjectInput{
		Bucket:      &cfg.s3Bucket,
		Key:         &s3Key,
		Body:        processed,
		ContentType: &mediatype,
	})
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to upload video file", err)
		return
	}

	videoURL := fmt.Sprint("https://", cfg.s3CfDistribution, ".cloudfront.net/", s3Key)
	metadata.VideoURL = &videoURL
	metadata.UpdatedAt = time.Now()
	err = cfg.db.UpdateVideo(metadata)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to update video", err)
		return
	}

	respondWithJSON(w, http.StatusOK, metadata)
}

func getVideoAspectRatio(filePath string) (AspectRatio, error) {
	ffCmd := exec.Command("ffprobe", "-v", "error", "-print_format", "json", "-show_streams", filePath)

	var buf bytes.Buffer
	ffCmd.Stdout = &buf

	err := ffCmd.Run()
	if err != nil {
		return "", err
	}

	var probe ffStreams
	err = json.Unmarshal(buf.Bytes(), &probe)
	if err != nil {
		return "", err
	}

	if len(probe.Streams) == 0 {
		return AspectRatioOther, nil
	}

	width, height := probe.Streams[0].Width, probe.Streams[0].Height
	if 9*(width/16) == height {
		return AspectRatio16_9, nil
	} else if 16*(width/9) == height {
		return AspectRatio9_16, nil
	} else {
		return AspectRatioOther, nil
	}
}

func processVideoForFastStart(filePath string) (string, error) {
	ffCmd := exec.Command("ffmpeg", "-i", filePath, "-c", "copy", "-movflags", "faststart", "-f", "mp4", filePath+".processing")

	if err := ffCmd.Run(); err != nil {
		return "", err
	} else {
		return filePath + ".processing", nil
	}
}
