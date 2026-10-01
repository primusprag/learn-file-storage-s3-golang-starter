package main

import (
	"fmt"
	"os"
	"strings"
)

func (cfg apiConfig) ensureAssetsDir() error {
	if _, err := os.Stat(cfg.assetsRoot); os.IsNotExist(err) {
		return os.Mkdir(cfg.assetsRoot, 0755)
	}
	return nil
}

func mediaTypeToExt(mediatype string) (string, error) {
	fileType := strings.Split(mediatype, "/")[1]
	if len(fileType) == 0 {
		return "", fmt.Errorf("Error splitting filetype")
	}
	return "." + fileType, nil
}

func getFileName(mediatype, videoIDString string) (string, error) {
	ext, err := mediaTypeToExt(mediatype)
	if err != nil {
		return "", err
	}
	return videoIDString + ext, nil
}
