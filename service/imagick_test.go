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

	resized, resizedWidth, resizedHeight, err := imageService.ImagickResizeWithDimensions(fixture, targetWidth, targetHeight)
	if err != nil {
		t.Fatalf("resize fixture: %v", err)
	}
	if len(resized) == 0 {
		t.Fatal("resize returned an empty image")
	}

	err, actualWidth, actualHeight := imageService.ImagickGetWidthHeight(resized)
	if err != nil {
		t.Fatalf("read resized dimensions: %v", err)
	}

	wantWidth, wantHeight := RatioWidthHeight(originalWidth, originalHeight, targetWidth, targetHeight)
	if resizedWidth != wantWidth || resizedHeight != wantHeight {
		t.Fatalf("unexpected helper dimensions: got %dx%d want %dx%d", resizedWidth, resizedHeight, wantWidth, wantHeight)
	}

	if actualWidth != resizedWidth || actualHeight != resizedHeight {
		t.Fatalf("helper dimensions do not match blob dimensions: helper=%dx%d blob=%dx%d", resizedWidth, resizedHeight, actualWidth, actualHeight)
	}
}
