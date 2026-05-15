package service

import (
	"os"
	"path/filepath"
	"testing"
)

func TestImagickResizeWithRepoFixture(t *testing.T) {
	fixturePath := filepath.Join("..", "public", "favicon.png")
	fixture, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read fixture %s: %v", fixturePath, err)
	}

	imageService := &ImageService{}

	err, originalWidth, originalHeight := imageService.ImagickGetWidthHeight(fixture)
	if err != nil {
		t.Fatalf("read original dimensions: %v", err)
	}

	const targetWidth uint = 16
	const targetHeight uint = 16

	resized := imageService.ImagickResize(fixture, targetWidth, targetHeight)
	if len(resized) == 0 {
		t.Fatal("resize returned an empty image")
	}

	err, resizedWidth, resizedHeight := imageService.ImagickGetWidthHeight(resized)
	if err != nil {
		t.Fatalf("read resized dimensions: %v", err)
	}

	wantWidth, wantHeight := RatioWidthHeight(originalWidth, originalHeight, targetWidth, targetHeight)
	if resizedWidth != wantWidth || resizedHeight != wantHeight {
		t.Fatalf("unexpected resized dimensions: got %dx%d want %dx%d", resizedWidth, resizedHeight, wantWidth, wantHeight)
	}
}
