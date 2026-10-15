package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	const maxMemory = 1 << 30
	_ = http.MaxBytesReader(w, r.Body, maxMemory)

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

	videoData, err := cfg.db.GetVideo(videoID)
	if err != nil || videoData.ID == uuid.Nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to fetch videos", err)
		return
	}

	if videoData.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "Unauthorized", err)
		return
	}

	file, header, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to parse form file", err)
		return
	}

	defer file.Close()

	mediatype, _, err := mime.ParseMediaType(header.Header.Get("Content-Type"))
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "unable to parser file type", err)
		return
	}
	if mediatype != "video/mp4" {
		respondWithError(w, http.StatusBadRequest, "Unsupported content type", err)
		return
	}

	tempfile, err := os.CreateTemp("", "tubely-upload_")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error creating temp file", err)
		return
	}
	defer os.Remove(tempfile.Name())
	defer tempfile.Close()

	_, err = io.Copy(tempfile, file)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error copying data to temp file", err)
		return
	}

	processedFileName, err := processVideoForFastStart(tempfile.Name())
	defer os.Remove(processedFileName)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error processing video for fast start", err)
		return
	}

	aspectRatio, err := getVideoAspectRatio(processedFileName)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, fmt.Sprintf("Unable to get aspect ratio: %s", err.Error()), err)
		return
	}

	processedFile, err := os.Open(processedFileName)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to open processed file", err)
		return
	}
	defer processedFile.Close()

	_, err = processedFile.Seek(0, io.SeekStart)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to reset offset of uploaded file", err)
		return
	}

	key := make([]byte, 32)
	rand.Read(key)
	fileName, err := getFileName(mediatype, base64.RawURLEncoding.EncodeToString(key))
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to create filename", err)
	}
	fileName = aspectRatio + "/" + fileName

	objectInput := s3.PutObjectInput{
		Bucket:      &cfg.s3Bucket,
		Key:         &fileName,
		Body:        io.Reader(processedFile),
		ContentType: &mediatype,
	}
	objectOutput, err := cfg.s3Client.PutObject(r.Context(), &objectInput)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to bucket file", err)
		return
	}

	videoURL := "https://" + cfg.s3Bucket + ".s3." + cfg.s3Region + ".amazonaws.com/" + fileName
	videoData.VideoURL = &videoURL
	cfg.db.UpdateVideo(videoData)

	fmt.Printf("Video uploaded to s3: %s\n", fileName)
	respondWithJSON(w, http.StatusOK, *objectOutput)
}

func getVideoAspectRatio(filePath string) (string, error) {
	if filePath == "" {
		return "", fmt.Errorf("File path cannot be empty")
	}
	cmd := exec.Command("ffprobe", "-v", "error", "-print_format", "json", "-show_streams", filePath)

	var buffer = bytes.Buffer{}
	cmd.Stdout = &buffer
	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("Unable to run %w", err)
	}

	type videoAspectRation struct {
		Streams []struct {
			Width  int `json:"width"`
			Height int `json:"height"`
		} `json:"streams"`
	}
	var aspectRatio = videoAspectRation{}
	err = json.Unmarshal(buffer.Bytes(), &aspectRatio)
	if err != nil {
		return "", fmt.Errorf("Error unmarshaling stdout: %w", err)
	}

	videoWidth := aspectRatio.Streams[0].Width
	videoHeight := aspectRatio.Streams[0].Height

	var landscape string = fmt.Sprintf("%.2f", 16.0/9.0)
	var portrait string = fmt.Sprintf("%.2f", 9.0/16.0)
	var aspectRatioFloat = fmt.Sprintf("%.2f", float32(videoWidth)/float32(videoHeight))

	switch aspectRatioFloat {
	case landscape:
		return "landscape", nil
	case portrait:
		return "portrait", nil
	default:
		return "other", nil
	}
}

func processVideoForFastStart(filePath string) (string, error) {
	outputFilePath := filePath + ".processing"

	cmd := exec.Command("ffmpeg", "-i", filePath, "-c", "copy", "-movflags", "faststart", "-f", "mp4", outputFilePath)
	err := cmd.Run()
	if err != nil {
		return "", err
	}

	return outputFilePath, nil
}
