# Serial reading

Serial-sync collects serialized writing from upstream sources and prepares it for reading. These terms distinguish the author's work from its releases and reading copies.

## Language

**Series**:
A reader-facing serial or franchise whose content belongs together, potentially across several sources or author books.
_Avoid_: Source, feed, book

**Author profile**:
Information that identifies and describes an author, potentially including a biography, portrait, and links to their public presence. It belongs to the author across their series.
_Avoid_: Series description, book metadata

**Cover**:
Artwork representing a series or an author book in the reading library. An author's portrait has a different role from a cover.
_Avoid_: Author portrait, post thumbnail

**Publication metadata**:
Descriptive information accompanying a reading copy, such as its title, author, series membership, reading order, synopsis, and artwork. It is distinct from a reader's progress through the story.
_Avoid_: Reading position, source credentials

**Release**:
One upstream publication, such as a creator's post and its attachments. A release may contain several chapters or no chapter at all.
_Avoid_: Chapter, EPUB

**Chapter**:
A unit of the story in reading order. Its identity and coverage are distinct from the release that supplies it.
_Avoid_: Post, release, file

**Author book**:
A division of a series identified by its author. Knowing its identity does not establish its complete chapter range.
_Avoid_: Volume, folder

**Volume**:
A reading copy that combines an ordered group of chapters. It may correspond to an author book or a configured chapter range.
_Avoid_: Author book, series

**Edition**:
A particular version of a reading copy, with its selected content and presentation. A correction can produce another edition of the same volume.
_Avoid_: New chapter, new book

**Reading position**:
The reader's current location within a chapter of a series. It is more precise than a chapter's read/unread status or a book's completion percentage.
_Avoid_: Read status, chapter number, completion percentage

**Correction**:
A revision to content already released, distinct from the arrival of another chapter.
_Avoid_: New chapter, new book

**Reading copy**:
A file delivered to the reading library: a single Chapter's file or a Volume.
_Avoid_: artifact, EPUB, output

**Release intake**:
The sync step that decides what a Release means for the library: nothing, a new reading copy, a Correction, or a rebuild it cannot do yet.
_Avoid_: handle, process, sync item

**Delivery**:
Publishing a reading copy to one target and retiring what it replaces.
_Avoid_: publish record, push, upload

**Run record**:
The log, events and payloads one run leaves behind, and what can be derived from them.
_Avoid_: logs, forensics
