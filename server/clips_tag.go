package main

// The stream's tag on a clip (A§4.14): the game, movie or show its stream was
// tagged with when it was cut — the streamer's What's on?, or else the title
// of their plan that was on then, exactly what apiStream.NowPlaying said at
// the click. The clipper's own words are the clip's title; this is the
// stream's, captured with the draft and kept with the clip, never edited
// afterwards. A stream with no tag gives a clip with none.
//
// Kept compact, because every clip carries it in the one clips document,
// whose cap is a megabyte: a category, a title, and when lookups found the
// real title, its ref, kind, year, link and source. Never the overview or
// the facts, which are a paragraph each and can be looked up again by ref.

// clipTag is the tag as a clip stores it and as every view shows it.
type clipTag struct {
	// "movie" or "game", as a plan's category is.
	Category string        `json:"category"`
	Title    string        `json:"title"`
	Media    *clipTagMedia `json:"media,omitempty"`
}

// clipTagMedia is the looked-up title behind a tag: scheduleMedia without the
// overview and the facts.
type clipTagMedia struct {
	Ref    string `json:"ref"`
	Kind   string `json:"kind"`
	Title  string `json:"title"`
	Year   string `json:"year,omitempty"`
	Link   string `json:"link,omitempty"`
	Source string `json:"source"`
}

// clipTagOf is the tag a draft captures from what is on its stream at the
// click, or nil for a stream with none.
//
// Cleaned like a clip's title: the stream's own key comes out first, while
// the whole of what was typed is there to find it in, and only then is it
// cut, so a key typed into What's on? never rides into a clip, half or whole.
// The media goes through checked() on the way in, as it does on the way out,
// and through the rule nowPlayingEntry.view keeps, that a title's kind
// matches the category; a tag whose media fails keeps its category and its
// title, and loses the media.
func clipTagOf(playing *apiNowPlaying, streamKey string) *clipTag {
	if playing == nil {
		return nil
	}

	tag := &clipTag{
		Category: playing.Category,
		Title:    redactStreamKey(playing.Title, streamKey, "the stream"),
	}
	if playing.Media != nil {
		media := compactTagMedia(playing.Media)
		media.Title = redactStreamKey(media.Title, streamKey, "the stream")
		tag.Media = media
	}

	return tag.shown()
}

// compactTagMedia drops what a clip does not keep of a looked-up title.
func compactTagMedia(media *scheduleMedia) *clipTagMedia {
	return &clipTagMedia{
		Ref:    media.Ref,
		Kind:   media.Kind,
		Title:  media.Title,
		Year:   media.Year,
		Link:   media.Link,
		Source: media.Source,
	}
}

// shown is the tag as it may be stored or shown, or nil: checked on the way
// out as well as on the way in, because the webapp trusts it, and the
// document it came from could have been written by a later build or by hand.
func (t *clipTag) shown() *clipTag {
	if t == nil {
		return nil
	}

	category := scheduleCategory(t.Category)
	title := cleanText(t.Title, maxMediaTitle)
	if category == "" || title == "" {
		return nil
	}

	return &clipTag{Category: category, Title: title, Media: t.Media.checked(category)}
}

// checked is the media as it may be shown, by the same rules as a plan's —
// scheduleMedia.checked — and of the tag's own category, or nil.
func (m *clipTagMedia) checked(category string) *clipTagMedia {
	if m == nil {
		return nil
	}

	full := &scheduleMedia{Ref: m.Ref, Kind: m.Kind, Title: m.Title, Year: m.Year, Link: m.Link, Source: m.Source}
	shown := full.checked()
	if shown == nil {
		return nil
	}
	if ref, ok := parseMediaRef(shown.Ref); !ok || ref.category() != category {
		return nil
	}

	return compactTagMedia(shown)
}

// kind is what a shared post says the tag is: the looked-up title's own kind
// — a show is "tv" — or else the category's.
func (t *clipTag) kind() string {
	if t.Media != nil {
		return t.Media.Kind
	}

	return t.Category
}
