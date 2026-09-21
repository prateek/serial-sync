package domain

func (s Sequence) HasChapter() bool { return s.Chapter != 0 || s.ChapterLabel != "" }
