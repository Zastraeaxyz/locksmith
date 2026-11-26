package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestFit(t *testing.T) {
	cases := []struct {
		width, height int
		wantW, wantH  int
	}{
		{1200, 1600, 96, 128},
		{1600, 1200, 128, 96},
		{128, 128, 128, 128},
		{0, 0, 128, 128},
		{1, 1600, 1, 128},
	}
	for _, tc := range cases {
		gotW, gotH := fit(tc.width, tc.height, 128)
		if gotW != tc.wantW || gotH != tc.wantH {
			t.Errorf("fit(%d, %d) = %d, %d, want %d, %d",
				tc.width, tc.height, gotW, gotH, tc.wantW, tc.wantH)
		}
	}
}

func TestValidEmojiName(t *testing.T) {
	for _, name := range []string{"blank", "green", "yellow_plus1", "go_again", "a1"} {
		if err := validEmojiName(name); err != nil {
			t.Errorf("validEmojiName(%q) = %v", name, err)
		}
	}
	for _, name := range []string{"", "a", "Blank", "go-again", "two words", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"} {
		if err := validEmojiName(name); err == nil {
			t.Errorf("validEmojiName(%q) = nil, want an error", name)
		}
	}
}

func TestAssetFiles(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"red.png", "green.png"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("png"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "svg"), 0o700); err != nil {
		t.Fatal(err)
	}

	files, err := assetFiles(dir)
	if err != nil {
		t.Fatalf("assetFiles: %v", err)
	}
	if len(files) != 2 || filepath.Base(files[0]) != "green.png" || filepath.Base(files[1]) != "red.png" {
		t.Fatalf("assetFiles = %v", files)
	}

	if _, err := assetFiles(t.TempDir()); err == nil {
		t.Fatal("empty directory should fail")
	}
}

func TestEmojiPNG(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 1200, 1600))
	for y := 0; y < 1600; y++ {
		for x := 0; x < 1200; x++ {
			src.SetNRGBA(x, y, color.NRGBA{R: 220, G: 30, B: 30, A: 255})
		}
	}

	path := filepath.Join(t.TempDir(), "blank.png")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, src); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	data, err := emojiPNG(path)
	if err != nil {
		t.Fatalf("emojiPNG: %v", err)
	}
	if len(data) > maxEmojiBytes {
		t.Fatalf("emoji is %d bytes, over the %d byte limit", len(data), maxEmojiBytes)
	}

	decoded, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decoding result: %v", err)
	}
	if got := decoded.Bounds(); got != image.Rect(0, 0, emojiSize, emojiSize) {
		t.Fatalf("bounds = %v, want 128x128", got)
	}

	if _, _, _, a := decoded.At(0, emojiSize/2).RGBA(); a != 0 {
		t.Errorf("side padding alpha = %d, want transparent", a)
	}
	if _, _, _, a := decoded.At(emojiSize/2, emojiSize/2).RGBA(); a == 0 {
		t.Error("card centre should be opaque")
	}
}
