package service

import (
	"log"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/mstgnz/cdn/pkg/config"
)

func MinioClient() *minio.Client {

	endpoint := config.GetEnvOrDefault("MINIO_ENDPOINT", "localhost:9000")
	accessKey := config.GetEnvOrDefault("MINIO_ROOT_USER", "minioadmin")
	secretKey := config.GetEnvOrDefault("MINIO_ROOT_PASSWORD", "minioadmin")

	minioClient, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: false,
	})
	if err != nil {
		log.Fatalln("MINIO CLIENT ERROR: ", err)
	}

	// Deliberately not `log.Printf("%#v", minioClient)`. That printed the whole
	// client struct, which today renders the credentials provider as a pointer
	// and therefore leaks nothing — but it is one upstream field-type change
	// away from writing the MinIO secret key to stdout on every boot, and it
	// was never readable enough to be worth that. The endpoint is the part
	// anyone actually wants when a connection fails.
	log.Printf("minio client ready: endpoint=%s bucket_policy_user=%s\n", endpoint, accessKey)

	return minioClient
}
