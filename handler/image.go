// Package handler /*
/*
## License
This project is licensed under the APACHE Licence. Refer to https://github.com/mstgnz/go-minio-cdn/blob/main/LICENSE for more information.
*/
package handler

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"github.com/mstgnz/cdn/pkg/batch"
	"github.com/mstgnz/cdn/pkg/config"
	"github.com/mstgnz/cdn/pkg/filetype"
	"github.com/mstgnz/cdn/pkg/validator"
	"github.com/mstgnz/cdn/pkg/worker"
	"github.com/mstgnz/cdn/service"
)

type Image interface {
	GetImage(c *fiber.Ctx) error
	UploadImage(c *fiber.Ctx) error
	DeleteImage(c *fiber.Ctx) error
	ResizeImage(c *fiber.Ctx) error
	UploadWithUrl(c *fiber.Ctx) error
	BatchUpload(c *fiber.Ctx) error
	BatchDelete(c *fiber.Ctx) error
}

type image struct {
	minioClient  *minio.Client
	awsService   service.AwsService
	imageService *service.ImageService
	cache        service.CacheService
	workerPool   *worker.Pool
	batchProc    *batch.BatchProcessor
}

// ImageProcessRequest represents an image processing request
type ImageProcessRequest struct {
	File        []byte
	Width       uint
	Height      uint
	ContentType string
	Filename    string
}

// UploadUrlRequest represents the request body for URL-based uploads
type UploadUrlRequest struct {
	Path      string `json:"path"`
	Bucket    string `json:"bucket" validate:"required"`
	URL       string `json:"url" validate:"required,url"`
	AWSUpload bool   `json:"aws_upload"`
}

// BatchUploadRequest represents the request body for batch uploads
type BatchUploadRequest struct {
	Bucket    string   `json:"bucket" validate:"required"`
	Path      string   `json:"path"`
	Files     []string `json:"files" validate:"required,min=1"`
	AWSUpload bool     `json:"aws_upload"`
}

// BatchDeleteRequest represents the request body for batch deletions
type BatchDeleteRequest struct {
	Bucket    string   `json:"bucket" validate:"required"`
	Files     []string `json:"files" validate:"required,min=1"`
	AWSDelete bool     `json:"aws_delete"`
}

func NewImage(minioClient *minio.Client, awsService service.AwsService, imageService *service.ImageService, cacheServices ...service.CacheService) Image {
	var cacheService service.CacheService
	if len(cacheServices) > 0 {
		cacheService = cacheServices[0]
	}

	// Initialize worker pool with 5 workers
	workerConfig := worker.DefaultConfig()
	workerConfig.Workers = 5
	wp := worker.NewPool(workerConfig)
	wp.Start()

	img := &image{
		minioClient:  minioClient,
		awsService:   awsService,
		imageService: imageService,
		cache:        cacheService,
		workerPool:   wp,
	}

	// Initialize batch processor with default config
	batchConfig := batch.DefaultConfig()
	batchConfig.BatchSize = 10
	batchConfig.FlushTimeout = 5 * time.Second
	bp := batch.NewBatchProcessor(batchConfig, img.processBatch)
	bp.Start()

	img.batchProc = bp

	return img
}

func getImageResizeRequest(c *fiber.Ctx) (bool, uint, uint) {
	if resize, width, height := service.GetWidthAndHeight(c, service.ParamsType); resize {
		return true, width, height
	}

	if resize, width, height := service.GetWidthAndHeight(c, service.QueryType); resize {
		return true, width, height
	}

	if c.Query("size") != "" {
		width, height := service.GetDimensions(c)
		return true, width, height
	}

	return false, 0, 0
}

func resizedImageCacheKey(bucket, objectName string, width, height uint) string {
	return fmt.Sprintf("resize:%s:%s:%d:%d", bucket, objectName, width, height)
}

func (i *image) sendCachedResizedImage(c *fiber.Ctx, ctx context.Context, bucket, objectName string, width, height uint) (bool, error) {
	if i.cache == nil {
		return false, nil
	}

	cachedImage, err := i.cache.GetResizedImage(bucket, objectName, width, height)
	if err != nil || len(cachedImage) == 0 {
		return false, nil
	}

	if _, err := i.minioClient.StatObject(ctx, bucket, objectName, minio.StatObjectOptions{}); err != nil {
		_ = i.cache.Delete(resizedImageCacheKey(bucket, objectName, width, height))
		return false, nil
	}

	if err, cachedWidth, cachedHeight := i.imageService.ImagickGetWidthHeight(cachedImage); err == nil {
		c.Set("Width", strconv.Itoa(int(cachedWidth)))
		c.Set("Height", strconv.Itoa(int(cachedHeight)))
	}

	c.Set("Content-Type", http.DetectContentType(cachedImage))
	c.Status(http.StatusOK)
	return true, c.Send(cachedImage)
}

func (i image) GetImage(c *fiber.Ctx) error {
	ctx := context.Background()
	bucket := c.Params("bucket")
	objectName := c.Params("*")

	var width uint
	var height uint
	resize := false

	if service.IsImageFile(objectName) {
		resize, width, height = getImageResizeRequest(c)
	} else {
		c.Status(400)
	}

	if resize {
		if sent, err := i.sendCachedResizedImage(c, ctx, bucket, objectName, width, height); sent || err != nil {
			return err
		}
	}

	object, err := i.minioClient.GetObject(ctx, bucket, objectName, minio.GetObjectOptions{})
	if err != nil {
		return c.SendFile("./public/notfound.png")
	}
	defer object.Close()

	getByte := service.StreamToByte(object)
	if len(getByte) == 0 {
		return c.SendFile("./public/notfound.png")
	}

	if service.IsImageFile(objectName) {
		if err, orjWidth, orjHeight := i.imageService.ImagickGetWidthHeight(getByte); err == nil {
			responseWidth, responseHeight := orjWidth, orjHeight
			if resize {
				responseWidth, responseHeight = service.RatioWidthHeight(orjWidth, orjHeight, width, height)
			}
			c.Set("Width", strconv.Itoa(int(responseWidth)))
			c.Set("Height", strconv.Itoa(int(responseHeight)))
		}
	}

	c.Set("Content-Type", http.DetectContentType(getByte))

	if resize {
		resizedImage := i.imageService.ImagickResize(getByte, width, height)
		if i.cache != nil && len(resizedImage) > 0 {
			_ = i.cache.SetResizedImage(bucket, objectName, width, height, resizedImage)
		}
		c.Status(http.StatusOK)
		return c.Send(resizedImage)
	}

	c.Status(http.StatusOK)
	return c.Send(getByte)
}

func (i image) UploadImage(c *fiber.Ctx) error {
	ctx := context.Background()

	path := c.FormValue("path")
	bucket := c.FormValue("bucket")
	file, err := c.FormFile("file")
	awsUpload := c.FormValue("aws_upload") == "true"

	if file == nil || err != nil {
		return service.Response(c, fiber.StatusBadRequest, false, "File Not Found!", nil)
	}

	// Check to see if the bucket already exists
	exists, err := i.minioClient.BucketExists(ctx, bucket)
	if err != nil && !exists {
		err = i.minioClient.MakeBucket(ctx, bucket, minio.MakeBucketOptions{})
		if err != nil {
			return service.Response(c, fiber.StatusBadRequest, false, "Bucket Not Found And Not Created!", nil)
		}
	}

	// Validate file metadata (extension, mime type, size from header)
	if err := validator.ValidateFile(file); err != nil {
		if valErr, ok := err.(*validator.FileValidationError); ok {
			return service.Response(c, fiber.StatusBadRequest, false, valErr.Message, map[string]string{
				"code": valErr.Code,
			})
		}
		return service.Response(c, fiber.StatusBadRequest, false, err.Error(), nil)
	}

	if awsUpload && !i.awsService.BucketExists(bucket) {
		return service.Response(c, fiber.StatusBadRequest, false, "Bucket Not Found On Aws S3!", nil)
	}

	// Stream the upload into a temp file instead of reading it all into memory
	fileBuffer, err := file.Open()
	if err != nil {
		return service.Response(c, fiber.StatusBadRequest, false, err.Error(), nil)
	}
	defer fileBuffer.Close()

	tempFile, err := os.CreateTemp("", "cdn_upload_*")
	if err != nil {
		return service.Response(c, fiber.StatusInternalServerError, false, "Failed to create temp file", nil)
	}
	tempPath := tempFile.Name()
	defer os.Remove(tempPath)

	if _, err := io.Copy(tempFile, fileBuffer); err != nil {
		tempFile.Close()
		return service.Response(c, fiber.StatusInternalServerError, false, "Failed to write temp file", nil)
	}
	tempFile.Close()

	// Parse the file name and extension
	parseFileName := strings.Split(file.Filename, ".")
	if len(parseFileName) < 2 {
		return service.Response(c, fiber.StatusBadRequest, false, "File extension not found!", nil)
	}

	// Generate random name and construct object name
	randomName := uuid.New().String()
	fileExtension := service.SanitizeObjectName(parseFileName[len(parseFileName)-1])
	imageName := randomName + "." + fileExtension
	objectName := imageName
	if path != "" {
		path = strings.Trim(path, "/")
		sanitizedPath := service.SanitizeObjectName(path)
		objectName = sanitizedPath + "/" + imageName
	}

	// Validate file content from the temp file (reads only the header from disk)
	if err := validator.ValidateFileContentFromPath(tempPath); err != nil {
		if valErr, ok := err.(*validator.FileValidationError); ok {
			return service.Response(c, fiber.StatusBadRequest, false, valErr.Message, map[string]string{
				"code": valErr.Code,
			})
		}
		return service.Response(c, fiber.StatusBadRequest, false, err.Error(), nil)
	}

	// Detect content type from the file on disk
	contentType, err := service.DetectContentTypeFromFile(tempPath)
	if err != nil {
		contentType = file.Header["Content-Type"][0]
	}

	info, err := os.Stat(tempPath)
	if err != nil {
		return service.Response(c, fiber.StatusInternalServerError, false, "Failed to stat temp file", nil)
	}
	fileSize := info.Size()

	// Get image dimensions and optionally resize — all via file paths
	if service.IsImageFile(file.Filename) {
		if err, orjWidth, orjHeight := i.imageService.ImagickGetWidthHeightFromFile(tempPath); err == nil {
			c.Set("Width", strconv.Itoa(int(orjWidth)))
			c.Set("Height", strconv.Itoa(int(orjHeight)))

			resize, width, height := service.GetWidthAndHeight(c, service.FormsType)
			if resize && orjWidth > 0 && orjHeight > 0 {
				width, height = service.RatioWidthHeight(orjWidth, orjHeight, width, height)
				resizedPath := tempPath + "_resized"
				if rw, rh, rSize, err := i.imageService.ImagickResizeFile(tempPath, resizedPath, width, height); err == nil {
					// Replace the temp file with the resized one
					os.Remove(tempPath)
					os.Rename(resizedPath, tempPath)
					fileSize = rSize
					c.Set("Width", strconv.Itoa(int(rw)))
					c.Set("Height", strconv.Itoa(int(rh)))
					c.Set("Content-Length", strconv.FormatInt(rSize, 10))
				} else {
					os.Remove(resizedPath)
				}
			}
		}
	}

	// Open the (possibly resized) temp file for upload
	uploadFile, err := os.Open(tempPath)
	if err != nil {
		return service.Response(c, fiber.StatusInternalServerError, false, "Failed to open file for upload", nil)
	}
	defer uploadFile.Close()

	// Minio Upload
	_, err = i.minioClient.PutObject(ctx, bucket, objectName, uploadFile, fileSize, minio.PutObjectOptions{ContentType: contentType})
	minioResult := "Minio Successfully Uploaded"

	if err != nil {
		return service.Response(c, fiber.StatusBadRequest, false, err.Error(), nil)
	}

	url := config.GetEnvOrDefault("APP_URL", "http://localhost:9090")
	url = strings.TrimSuffix(url, "/")
	link := url + "/" + bucket + "/" + objectName

	// S3 Upload
	if awsUpload {
		awsResult := "S3 Successfully Uploaded"
		uploadFile.Seek(0, 0)
		if _, err = i.awsService.S3PutObject(bucket, objectName, uploadFile); err != nil {
			awsResult = fmt.Sprintf("S3 Failed Uploaded %s", err.Error())
		}
		return service.Response(c, fiber.StatusCreated, true, "success", map[string]any{
			"minioUpload": fmt.Sprintf("Minio Successfully Uploaded size %d", fileSize),
			"minioResult": minioResult,
			"awsUpload":   awsResult,
			"awsResult":   awsResult,
			"imageName":   imageName,
			"objectName":  objectName,
			"link":        link,
		})
	}

	return service.Response(c, fiber.StatusCreated, true, "success", map[string]any{
		"minioUpload": fmt.Sprintf("Minio Successfully Uploaded size %d", fileSize),
		"minioResult": minioResult,
		"awsUpload":   "",
		"awsResult":   "",
		"imageName":   imageName,
		"objectName":  objectName,
		"link":        link,
	})
}

func (i image) UploadWithUrl(c *fiber.Ctx) error {
	ctx := context.Background()

	// Parse request body
	var req UploadUrlRequest
	if err := c.BodyParser(&req); err != nil {
		return service.Response(c, fiber.StatusBadRequest, false, "Invalid request body", nil)
	}

	// Validate request
	if err := validator.ValidateStruct(req); err != nil {
		return service.Response(c, fiber.StatusBadRequest, false, err.Error(), nil)
	}

	// Check to see if already exist bucket
	exists, err := i.minioClient.BucketExists(ctx, req.Bucket)
	if err != nil && !exists {
		// Bucket not found so Make a new bucket
		err = i.minioClient.MakeBucket(ctx, req.Bucket, minio.MakeBucketOptions{})
		if err != nil {
			return service.Response(c, fiber.StatusBadRequest, false, "Bucket Not Found And Not Created!", nil)
		}
	}

	// Check if the AWS bucket exists if required
	if req.AWSUpload && !i.awsService.BucketExists(req.Bucket) {
		return service.Response(c, fiber.StatusBadRequest, false, "Bucket Not Found On Aws S3!", nil)
	}

	httpClient := &http.Client{Timeout: 30 * time.Second}
	res, err := httpClient.Get(req.URL)
	if err != nil {
		return service.Response(c, fiber.StatusBadRequest, false, err.Error(), nil)
	}
	defer res.Body.Close()

	// Read content from URL, capped at the configured max file size
	maxSize := config.GetEnvAsIntOrDefault("MAX_FILE_SIZE", int(validator.DefaultMaxFileSize))
	content, err := io.ReadAll(io.LimitReader(res.Body, int64(maxSize)))
	if err != nil {
		return service.Response(c, fiber.StatusBadRequest, false, "Failed to read content from URL", nil)
	}

	// Automatically detect content type
	contentType := http.DetectContentType(content)

	// Determine file extension from content type
	extension := filetype.GetExtensionFromContentType(contentType)
	if extension == "" {
		// Try to extract extension from URL if content type is not recognized
		extension = filetype.GetExtensionFromURL(req.URL)
		if !filetype.IsValidExtension(extension) {
			return service.Response(c, fiber.StatusBadRequest, false, "Unsupported or unrecognized file type", nil)
		}
	}

	randomName := uuid.New().String()
	// Sanitize extension
	sanitizedExtension := service.SanitizeObjectName(extension)
	objectName := randomName + "." + sanitizedExtension
	if req.Path != "" {
		// Sanitize path as well
		req.Path = strings.Trim(req.Path, "/")
		sanitizedPath := service.SanitizeObjectName(req.Path)
		objectName = sanitizedPath + "/" + randomName + "." + sanitizedExtension
	}

	// Prepare content as a new reader
	contentReader := bytes.NewReader(content)

	// Upload with PutObject
	minioResult, err := i.minioClient.PutObject(ctx, req.Bucket, objectName, contentReader, int64(len(content)), minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		return service.Response(c, fiber.StatusBadRequest, false, err.Error(), nil)
	}

	url := config.GetEnvOrDefault("APP_URL", "http://localhost:9090")
	url = strings.TrimSuffix(url, "/")
	link := url + "/" + req.Bucket + "/" + objectName

	// S3 upload with glacier storage class
	awsResult := "S3 Successfully Uploaded"
	if req.AWSUpload {
		contentReader.Seek(0, 0) // Reset reader to beginning
		_, err := i.awsService.S3PutObject(req.Bucket, objectName, contentReader)
		if err != nil {
			awsResult = fmt.Sprintf("S3 Failed Uploaded %s", err.Error())
		}
		return service.Response(c, fiber.StatusCreated, true, "success", map[string]any{
			"minioUpload": fmt.Sprintf("Minio Successfully Uploaded size %d", minioResult.Size),
			"minioResult": minioResult,
			"awsUpload":   awsResult,
			"awsResult":   awsResult,
			"imageName":   randomName + "." + extension,
			"objectName":  objectName,
			"link":        link,
		})
	}

	return service.Response(c, fiber.StatusCreated, true, "success", map[string]any{
		"minioUpload": fmt.Sprintf("Minio Successfully Uploaded size %d", minioResult.Size),
		"minioResult": minioResult,
		"awsUpload":   "",
		"awsResult":   "",
		"imageName":   randomName + "." + extension,
		"objectName":  objectName,
		"link":        link,
	})
}

// DeleteImage handles image deletion
func (i image) DeleteImage(c *fiber.Ctx) error {
	ctx := context.Background()
	bucket := c.Params("bucket")
	awsDelete := c.Params("aws_delete") == "true"
	object := c.Params("*")

	if len(bucket) == 0 || len(object) == 0 {
		return service.Response(c, fiber.StatusBadRequest, false, "invalid path or bucket or file.", nil)
	}

	// Check if the bucket exists on Minio
	if found, _ := i.minioClient.BucketExists(ctx, bucket); !found {
		return service.Response(c, fiber.StatusBadRequest, false, "Bucket Not Found On Minio!", "")
	}

	// Check if the bucket exists on AWS S3 if required
	if awsDelete && !i.awsService.BucketExists(bucket) {
		return service.Response(c, fiber.StatusBadRequest, false, "Bucket Not Found On Aws S3!", "")
	}

	// Remove object from Minio
	if err := i.minioClient.RemoveObject(ctx, bucket, object, minio.RemoveObjectOptions{}); err != nil {
		return service.Response(c, fiber.StatusInternalServerError, false, err.Error(), "")
	}

	// Remove object from AWS S3 if required
	if awsDelete {
		if err := i.awsService.DeleteObjects(bucket, []string{object}); err != nil {
			return service.Response(c, fiber.StatusInternalServerError, false, err.Error(), "")
		}
	}

	return service.Response(c, fiber.StatusOK, true, "File Successfully Deleted", "")
}

// ResizeImage handles image resizing using worker pool
func (i *image) ResizeImage(c *fiber.Ctx) error {
	resize, width, height := service.GetWidthAndHeight(c, service.FormsType)
	file, err := c.FormFile("file")

	if file == nil || err != nil {
		return service.Response(c, fiber.StatusBadRequest, false, "File Not Found!", nil)
	}

	fileBuffer, err := file.Open()
	if err != nil {
		return service.Response(c, fiber.StatusBadRequest, false, err.Error(), nil)
	}
	defer fileBuffer.Close()

	fileContent, err := io.ReadAll(fileBuffer)
	if err != nil {
		return service.Response(c, fiber.StatusInternalServerError, false, "Error reading file content", nil)
	}

	if !resize || !service.IsImageFile(file.Filename) {
		c.Set("Content-Length", strconv.Itoa(len(fileContent)))
		c.Set("Content-Type", http.DetectContentType(fileContent))
		return c.Send(fileContent)
	}

	// Create response channel
	respChan := make(chan error, 1)

	// Create and submit job
	job := worker.Job{
		ID: uuid.New().String(),
		Task: func() error {
			req := &ImageProcessRequest{
				File:        fileContent,
				Width:       uint(width),
				Height:      uint(height),
				ContentType: file.Header.Get("Content-Type"),
				Filename:    file.Filename,
			}
			return processImage(req, i)
		},
		Response: respChan,
	}

	if err := i.workerPool.Submit(job); err != nil {
		return service.Response(c, fiber.StatusServiceUnavailable, false, "Image processing queue is full", nil)
	}

	// Wait for response
	if err := <-respChan; err != nil {
		return service.Response(c, fiber.StatusInternalServerError, false, "Image processing failed", nil)
	}

	return service.Response(c, fiber.StatusOK, true, "Image processed successfully", nil)
}

// processBatch handles batch processing of items
func (i *image) processBatch(items []batch.BatchItem) []batch.BatchItem {
	// Process items in parallel using goroutines
	var wg sync.WaitGroup
	for idx := range items {
		wg.Add(1)
		go func(item *batch.BatchItem) {
			defer wg.Done()

			// Process the item based on its type
			switch data := item.Data.(type) {
			case *ImageProcessRequest:
				// Process image
				err := processImage(data, i)
				item.Error = err
				item.Success = err == nil
			}
		}(&items[idx])
	}
	wg.Wait()
	return items
}

// processImage handles the actual image processing
func processImage(req *ImageProcessRequest, i *image) error {
	if service.IsImageFile(req.Filename) {
		resized := i.imageService.ImagickResize(req.File, req.Width, req.Height)
		if resized == nil {
			return fmt.Errorf("image processing failed")
		}
		return nil
	}
	return nil
}

// BatchUpload handles multiple file uploads
func (i *image) BatchUpload(c *fiber.Ctx) error {
	form, err := c.MultipartForm()
	if err != nil {
		return service.Response(c, fiber.StatusBadRequest, false, "Invalid form data", nil)
	}

	bucket := form.Value["bucket"]
	if len(bucket) == 0 {
		return service.Response(c, fiber.StatusBadRequest, false, "Bucket is required", nil)
	}

	path := form.Value["path"]
	pathPrefix := ""
	if len(path) > 0 {
		pathPrefix = path[0]
	}

	awsUpload := form.Value["aws_upload"] != nil && form.Value["aws_upload"][0] == "true"

	// Check bucket existence
	exists, err := i.minioClient.BucketExists(context.Background(), bucket[0])
	if err != nil || !exists {
		return service.Response(c, fiber.StatusBadRequest, false, "Bucket not found", nil)
	}

	// Check AWS bucket if needed
	if awsUpload && !i.awsService.BucketExists(bucket[0]) {
		return service.Response(c, fiber.StatusBadRequest, false, "AWS bucket not found", nil)
	}

	files := form.File["files"]
	if len(files) == 0 {
		return service.Response(c, fiber.StatusBadRequest, false, "No files provided", nil)
	}

	results := make([]map[string]any, 0)
	var wg sync.WaitGroup
	resultChan := make(chan map[string]any, len(files))
	sem := make(chan struct{}, 10)

	for _, file := range files {
		wg.Add(1)
		sem <- struct{}{}
		go func(file *multipart.FileHeader) {
			defer func() { <-sem; wg.Done() }()

			result := make(map[string]any)
			result["filename"] = file.Filename

			// Validate file
			if err := validator.ValidateFile(file); err != nil {
				result["success"] = false
				result["error"] = err.Error()
				resultChan <- result
				return
			}

			// Process and upload file
			fileContent, err := file.Open()
			if err != nil {
				result["success"] = false
				result["error"] = err.Error()
				resultChan <- result
				return
			}
			defer fileContent.Close()

			// Generate object name
			randomName := uuid.New().String()
			// Sanitize filename
			sanitizedFilename := service.SanitizeObjectName(file.Filename)
			objectName := randomName + "_" + sanitizedFilename
			if pathPrefix != "" {
				// Sanitize path prefix as well
				sanitizedPath := service.SanitizeObjectName(pathPrefix)
				objectName = sanitizedPath + "/" + objectName
			}

			// Upload to MinIO
			contentType := file.Header.Get("Content-Type")
			_, err = i.minioClient.PutObject(
				context.Background(),
				bucket[0],
				objectName,
				fileContent,
				file.Size,
				minio.PutObjectOptions{ContentType: contentType},
			)

			if err != nil {
				result["success"] = false
				result["error"] = err.Error()
				resultChan <- result
				return
			}

			// Upload to AWS if requested
			if awsUpload {
				fileContent.Seek(0, 0)
				_, err = i.awsService.S3PutObject(bucket[0], objectName, fileContent)
				if err != nil {
					result["aws_error"] = err.Error()
				}
			}

			result["success"] = true
			result["object_name"] = objectName
			resultChan <- result
		}(file)
	}

	// Wait for all uploads to complete
	go func() {
		wg.Wait()
		close(resultChan)
	}()

	// Collect results
	for result := range resultChan {
		results = append(results, result)
	}

	return service.Response(c, fiber.StatusOK, true, "Batch upload completed", results)
}

// BatchDelete handles multiple file deletions
func (i *image) BatchDelete(c *fiber.Ctx) error {
	var req BatchDeleteRequest
	if err := c.BodyParser(&req); err != nil {
		return service.Response(c, fiber.StatusBadRequest, false, "Invalid request body", nil)
	}

	if err := validator.ValidateStruct(req); err != nil {
		return service.Response(c, fiber.StatusBadRequest, false, err.Error(), nil)
	}

	// Check bucket existence
	exists, err := i.minioClient.BucketExists(context.Background(), req.Bucket)
	if err != nil || !exists {
		return service.Response(c, fiber.StatusBadRequest, false, "Bucket not found", nil)
	}

	// Check AWS bucket if needed
	if req.AWSDelete && !i.awsService.BucketExists(req.Bucket) {
		return service.Response(c, fiber.StatusBadRequest, false, "AWS bucket not found", nil)
	}

	results := make([]map[string]any, 0)
	var wg sync.WaitGroup
	resultChan := make(chan map[string]any, len(req.Files))
	sem := make(chan struct{}, 10)

	for _, file := range req.Files {
		wg.Add(1)
		sem <- struct{}{}
		go func(filename string) {
			defer func() { <-sem; wg.Done() }()

			result := make(map[string]any)
			result["filename"] = filename

			// Delete from MinIO
			err := i.minioClient.RemoveObject(context.Background(), req.Bucket, filename, minio.RemoveObjectOptions{})
			if err != nil {
				result["success"] = false
				result["error"] = err.Error()
				resultChan <- result
				return
			}

			// Delete from AWS if requested
			if req.AWSDelete {
				if err := i.awsService.DeleteObjects(req.Bucket, []string{filename}); err != nil {
					result["aws_error"] = err.Error()
				}
			}

			result["success"] = true
			resultChan <- result
		}(file)
	}

	// Wait for all deletions to complete
	go func() {
		wg.Wait()
		close(resultChan)
	}()

	// Collect results
	for result := range resultChan {
		results = append(results, result)
	}

	return service.Response(c, fiber.StatusOK, true, "Batch delete completed", results)
}
