package main

import (
	"context"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/database"
)

func generatePresignedURL(s3Client *s3.Client, bucket, key string, expireTime time.Duration) (string, error) {
	presignClient := s3.NewPresignClient(s3Client)
	obj, err := presignClient.PresignGetObject(context.TODO(), &s3.GetObjectInput{
		Bucket: &bucket,
		Key:    &key,
	}, s3.WithPresignExpires(expireTime))
	if err != nil {
		return "", err
	}
	return obj.URL, nil
}

func (cfg *apiConfig) dbVideoToSignedVideo(video database.Video) (database.Video, error) {
	if video.VideoURL == nil {
		return video, nil
	}

	urlComps := strings.Split(*video.VideoURL, ",")
	if len(urlComps) < 2 {
		return video, nil
	}

	bucket, key := urlComps[0], urlComps[1]
	videoURL, err := generatePresignedURL(cfg.s3Client, bucket, key, time.Hour*12)
	if err != nil {
		return video, err
	}

	video.VideoURL = &videoURL
	return video, nil
}
