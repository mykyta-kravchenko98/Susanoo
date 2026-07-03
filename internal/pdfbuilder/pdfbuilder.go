package pdfbuilder

import (
	"bytes"
	"fmt"
	"io"

	"github.com/pdfcpu/pdfcpu/pkg/api"
)

func BuildFromJPEGs(images [][]byte) ([]byte, error) {
	if len(images) == 0 {
		return nil, fmt.Errorf("no images provided to build PDF from")
	}

	readers := make([]io.Reader, len(images))
	for i, img := range images {
		readers[i] = bytes.NewReader(img)
	}

	var out bytes.Buffer
	// rs=nil -> create a new PDF from scratch (do not append to an existing one).
	// imp=nil -> default import configuration (page size = image size).
	// conf=nil -> default pdfcpu configuration.
	if err := api.ImportImages(nil, &out, readers, nil, nil); err != nil {
		return nil, fmt.Errorf("import images into pdf: %w", err)
	}
	return out.Bytes(), nil
}