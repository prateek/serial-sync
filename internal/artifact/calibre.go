package artifact

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func convertPDFToEPUB(ctx context.Context, pdfContent []byte, title, author string) ([]byte, error) {
	converterPath, err := exec.LookPath("ebook-convert")
	if err != nil {
		return nil, fmt.Errorf("ebook-convert is required for PDF to EPUB conversion: %w", err)
	}

	workDir, err := os.MkdirTemp("", "serial-sync-calibre-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(workDir)

	inputPath := filepath.Join(workDir, "input.pdf")
	outputPath := filepath.Join(workDir, "output.epub")
	if err := os.WriteFile(inputPath, pdfContent, 0o644); err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(
		ctx,
		converterPath,
		inputPath,
		outputPath,
		"--title", title,
		"--authors", author,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("ebook-convert failed: %w: %s", err, string(output))
	}

	epubContent, err := os.ReadFile(outputPath)
	if err != nil {
		return nil, err
	}
	return epubContent, nil
}
