package sequence

import (
	"fmt"
	"sort"

	"github.com/prateek/serial-sync/internal/classify"
	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
)

func Apply(cfg *config.Config, sourceID string, release domain.NormalizedRelease, decision domain.TrackDecision) domain.TrackDecision {
	seq := Detect(release.Title)
	seq.Origin = "title"
	if decision.ContentStrategy == domain.ContentStrategyAttachmentOnly || decision.ContentStrategy == domain.ContentStrategyAttachmentPreferred {
		if attachment, ok := classify.SelectAttachment(release, decision); ok {
			combined := Detect(release.Title, attachment.FileName)
			if !seq.HasChapter() && seq.Part == "" && (combined.HasChapter() || combined.Part != "") {
				combined.Origin = "attachment"
			} else {
				combined.Origin = "title"
			}
			seq = combined
		}
	}
	return ApplyDetected(cfg, sourceID, release.ProviderReleaseID, decision, seq)
}

func ApplyDetected(cfg *config.Config, sourceID, releaseID string, decision domain.TrackDecision, sequence domain.Sequence) domain.TrackDecision {
	series := config.SeriesConfig{}
	for _, item := range cfg.Series {
		if item.ID == decision.SeriesID {
			series = item
			break
		}
	}
	bookID := decision.BookID
	for _, override := range series.SequenceOverrides {
		if override.Source != sourceID || override.ReleaseID != releaseID {
			continue
		}
		sequence.Origin = "override"
		sequence.KeepSingle = sequence.KeepSingle || override.KeepSingle
		if override.Chapter > 0 {
			sequence.Chapter = override.Chapter
			sequence.KeepSingle = override.KeepSingle
			sequence.ChapterLabel, sequence.Part, sequence.Reason = "", "", ""
			sequence.MatchedText = ""
		}
		if override.BookID != "" {
			bookID = override.BookID
		}
	}
	sequence = Resolve(series, sequence, bookID)
	if sequence.KeepSingle && sequence.Reason == "" {
		sequence.Reason = "explicitly kept as a single"
	}
	decision.Sequence = &sequence
	return decision
}

func Resolve(series config.SeriesConfig, sequence domain.Sequence, bookID string) domain.Sequence {
	extended := sequence.ChapterLabel != "" || sequence.Part != ""
	sequence.Position = 0
	if !extended {
		sequence.Position = sequence.Chapter
	}
	if bookID != "" {
		sequence.BookID = bookID
		if sequence.Origin != "override" {
			sequence.Origin = "input"
		}
	}
	if sequence.Book == 0 && bookID == "" {
		if series.Output.FinalChapter > 0 && sequence.Chapter > series.Output.FinalChapter {
			sequence.KeepSingle = true
			sequence.Reason = "chapter exceeds final_chapter"
		}
		return sequence
	}
	sequence.Position = 0
	books := append([]config.BookConfig(nil), series.Books...)
	sort.Slice(books, func(i, j int) bool { return books[i].Number < books[j].Number })
	position := 1
	for index, book := range books {
		first := book.FirstChapter
		if first == 0 {
			first = 1
		}
		if book.SeriesPositionStart > 0 {
			position = book.SeriesPositionStart
		} else if first > 1 {
			position = first
		} else if index == 0 && book.Number != 1 || index > 0 && book.Number != books[index-1].Number+1 {
			position = 0
		}
		if book.ID == bookID || bookID == "" && book.Number == sequence.Book {
			sequence.BookID = book.ID
			sequence.Book = book.Number
			if sequence.Chapter < first || book.LastChapter > 0 && sequence.Chapter > book.LastChapter {
				sequence.KeepSingle = true
				sequence.Reason = "chapter is outside the declared book range"
				return sequence
			}
			if position > 0 && sequence.Chapter >= first && !extended {
				sequence.Position = position + sequence.Chapter - first
			}
			for _, later := range books[index+1:] {
				start := later.SeriesPositionStart
				if start == 0 && later.FirstChapter > 1 {
					start = later.FirstChapter
				}
				if start > 0 && sequence.Position >= start {
					sequence.Position = 0
					sequence.KeepSingle = true
					sequence.Reason = "chapter overlaps the series position range of book " + later.ID
					return sequence
				}
			}
			if sequence.Position == 0 && !extended {
				sequence.Reason = "missing earlier book range or series_position_start"
			}
			return sequence
		}
		if book.LastChapter < first {
			position = 0
		} else if position > 0 {
			position += book.LastChapter - first + 1
		}
	}
	if sequence.BookID == "" {
		sequence.BookID = fmt.Sprintf("book:%d", sequence.Book)
	}
	sequence.Reason = "book requires an explicit expected chapter range"
	return sequence
}
