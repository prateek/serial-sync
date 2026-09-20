package config

import (
	"fmt"
	"sort"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

func ValidateExplicitOutputFields(data []byte) error {
	var document struct {
		Series []struct {
			Output struct {
				ChaptersPerVolume *int `toml:"chapters_per_volume"`
			} `toml:"output"`
		} `toml:"series"`
	}
	if err := toml.Unmarshal(data, &document); err != nil {
		return err
	}
	for _, series := range document.Series {
		if size := series.Output.ChaptersPerVolume; size != nil && *size <= 0 {
			return fmt.Errorf("chapters_per_volume must be positive when supplied")
		}
	}
	return nil
}

func validateReaderOutput(series SeriesConfig, sources map[string]struct{}) error {
	output := SeriesOutputDefaults(series.Output)
	if output.Bundling != "none" && output.Bundling != "volume" {
		return fmt.Errorf("bundling must be none or volume")
	}
	if output.Bundling == "volume" && output.Format != "epub" {
		return fmt.Errorf("volume bundling requires format = epub")
	}
	if output.ChaptersPerVolume < 1 {
		return fmt.Errorf("chapters_per_volume must be positive")
	}
	if output.FinalChapter < 0 {
		return fmt.Errorf("final_chapter must be positive when supplied")
	}
	if err := validateGaps(output.IntentionalGaps, 1, output.FinalChapter); err != nil {
		return err
	}
	books := append([]BookConfig(nil), series.Books...)
	sort.Slice(books, func(i, j int) bool { return books[i].Number < books[j].Number })
	ids, numbers := map[string]bool{}, map[int]bool{}
	previousEnd := 0
	for _, book := range books {
		if strings.TrimSpace(book.ID) == "" || ids[book.ID] {
			return fmt.Errorf("book id must be present and unique: %q", book.ID)
		}
		if book.Number < 1 || numbers[book.Number] {
			return fmt.Errorf("book number must be positive and unique: %d", book.Number)
		}
		ids[book.ID], numbers[book.Number] = true, true
		first := book.FirstChapter
		if first == 0 {
			first = 1
		}
		if first < 1 || book.LastChapter < 0 || book.LastChapter > 0 && book.LastChapter < first {
			return fmt.Errorf("book %s has an invalid chapter range", book.ID)
		}
		if book.SeriesPositionStart < 0 {
			return fmt.Errorf("book %s series_position_start must be positive", book.ID)
		}
		start := book.SeriesPositionStart
		if start == 0 {
			start = previousEnd + 1
			if first > 1 {
				start = first
			}
		}
		if start <= previousEnd {
			return fmt.Errorf("book %s overlaps an earlier series position range", book.ID)
		}
		previousEnd = start
		if book.LastChapter > 0 {
			previousEnd = start + book.LastChapter - first
		}
		if err := validateGaps(book.IntentionalGaps, first, book.LastChapter); err != nil {
			return fmt.Errorf("book %s: %w", book.ID, err)
		}
	}
	for _, input := range series.Inputs {
		if input.BookID != "" && !ids[input.BookID] {
			return fmt.Errorf("input references unknown book_id %q", input.BookID)
		}
		if input.AnthologyMode != nil && *input.AnthologyMode {
			return fmt.Errorf("anthology_mode = true is unsupported; use series.output bundling = volume with format = epub")
		}
	}
	overrides := map[string]bool{}
	slots := map[string]bool{}
	for _, override := range series.SequenceOverrides {
		if _, ok := sources[override.Source]; !ok {
			return fmt.Errorf("sequence override references unknown source %q", override.Source)
		}
		key := override.Source + "/" + override.ReleaseID
		if strings.TrimSpace(override.ReleaseID) == "" || overrides[key] {
			return fmt.Errorf("sequence override release_id must be present and unique per source")
		}
		overrides[key] = true
		if override.BookID != "" && !ids[override.BookID] {
			return fmt.Errorf("sequence override references unknown book_id %q", override.BookID)
		}
		if override.Chapter < 0 {
			return fmt.Errorf("sequence override chapter must be positive")
		}
		if override.Chapter == 0 && override.BookID == "" && !override.KeepSingle {
			return fmt.Errorf("sequence override must set chapter, book_id or keep_single")
		}
		if override.Chapter > 0 && !override.KeepSingle {
			slot := fmt.Sprintf("%s/%d", override.BookID, override.Chapter)
			if slots[slot] {
				return fmt.Errorf("overlapping sequence overrides for chapter slot %s", slot)
			}
			slots[slot] = true
		}
	}
	return nil
}

func validateGaps(gaps []ChapterGap, first, last int) error {
	seen := map[int]bool{}
	for _, gap := range gaps {
		if gap.Chapter < first || last > 0 && gap.Chapter > last || seen[gap.Chapter] {
			return fmt.Errorf("intentional gap chapter must be unique and within its expected range")
		}
		if strings.TrimSpace(gap.Reason) == "" {
			return fmt.Errorf("intentional gap requires a reason")
		}
		seen[gap.Chapter] = true
	}
	return nil
}
