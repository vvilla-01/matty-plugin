package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
)

// The stream's tag on a clip (A§4.14): what was on the stream when it was
// cut, kept compact with the draft and the clip, checked on the way in and on
// the way out, carried by a shared post's props for its card, never in its
// message, and taken off the post with the title when the clip is deleted.

// Titles looked up, as a plan or What's on? carries them, overview and facts
// included.
func eldenRing() *scheduleMedia {
	return &scheduleMedia{
		Ref:      "steam:1245620",
		Kind:     mediaKindGame,
		Title:    "ELDEN RING",
		Year:     "2022",
		Overview: strings.Repeat("Rise, Tarnished. ", 40),
		Facts:    "FromSoftware · Action RPG",
		Link:     "https://store.steampowered.com/app/1245620/",
		Source:   mediaSourceSteam,
	}
}

func heat() *scheduleMedia {
	return &scheduleMedia{
		Ref:      "tmdb:movie:949",
		Kind:     mediaKindMovie,
		Title:    "Heat",
		Year:     "1995",
		Overview: strings.Repeat("A group of high-end professional thieves. ", 20),
		Facts:    "Michael Mann · 170 min",
		Link:     "https://www.imdb.com/title/tt0113277/",
		Source:   mediaSourceTMDB,
	}
}

func theBear() *scheduleMedia {
	return &scheduleMedia{
		Ref:    "tmdb:tv:136315",
		Kind:   mediaKindTV,
		Title:  "The Bear",
		Year:   "2022",
		Link:   "https://www.themoviedb.org/tv/136315",
		Source: mediaSourceTMDB,
	}
}

// compactOf is a looked-up title as a clip keeps it.
func compactOf(media *scheduleMedia) *clipTagMedia {
	return &clipTagMedia{Ref: media.Ref, Kind: media.Kind, Title: media.Title, Year: media.Year, Link: media.Link, Source: media.Source}
}

// A draft takes what was on its stream at the click — the stream's own key
// taken out of what was typed — and keeps it, whatever is on afterwards: the
// clip published from it, its card and its row in the System Console say the
// same. A stream with nothing on gives a clip with no tag, and nothing for it
// in any answer. A draft cut from the held end of a stream takes what was on
// it as it ended, which the tail kept (A§4.14; that the recorder manager
// hands a tail the stream's tag from the poll is
// TestEndedStreamCanBeClippedFromItsTail).
func TestDraftCapturesTheStreamsTag(t *testing.T) {
	d := newDraftsWorld(t)
	d.goLive(apiStream{
		StreamKey: "videokey-dave", Title: "Dave", UserID: "uid-dave", Username: "dave", LinkSource: "manual",
		NowPlaying: &apiNowPlaying{Category: categoryGame, Title: "ELDEN RING on videokey-dave", Media: eldenRing()},
	})

	response := d.cut(t, "uid-bob", d.dave, clipSourceLive)
	draft := decodeDraft(t, response, http.StatusCreated)
	want := &clipTag{Category: categoryGame, Title: "ELDEN RING on the stream", Media: compactOf(eldenRing())}
	require.Equal(t, want, draft.Tag)
	for _, left := range []string{"overview", "facts", "Tarnished", "FromSoftware", "videokey-dave"} {
		require.NotContains(t, response.Body.String(), left)
	}

	// What is on changes after the click; the draft keeps what was on then.
	d.goLive(apiStream{
		StreamKey: "videokey-dave", Title: "Dave", UserID: "uid-dave", Username: "dave", LinkSource: "manual",
		NowPlaying: &apiNowPlaying{Category: categoryMovie, Title: "Heat", Media: heat()},
	})
	require.Equal(t, want, d.drafts(t, "uid-bob")[0].Tag)

	view := decodeClipView(t, d.publish(t, "uid-bob", draft.ID, publishBody("that jump", 2000, 12000)), http.StatusCreated)
	require.Equal(t, want, view.Tag)
	require.Equal(t, want, d.stored(t).clip(draft.ID).Tag)
	require.NotContains(t, string(d.kvGet(kvClips)), "Tarnished")

	response = d.call(t, "uid-dan", clipCall{method: http.MethodGet, target: clipsPath + "/cards?ids=" + draft.ID})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var cards clipCardsResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &cards))
	require.Equal(t, want, cards.Cards[draft.ID].Tag)

	response = d.call(t, "uid-dan", clipCall{method: http.MethodGet, target: clipsPath + "/clip?id=" + draft.ID})
	require.Equal(t, want, decodeClipView(t, response, http.StatusOK).Tag)

	console := decodeAdminClips(t, d.call(t, "uid-boss", clipCall{method: http.MethodGet, target: adminClipsPath}))
	require.Len(t, console.Clips, 1)
	require.Equal(t, want, console.Clips[0].Tag)

	// Typed with nothing looked up: its category and its words.
	d.goLive(apiStream{
		StreamKey: "videokey-dave", Title: "Dave", UserID: "uid-dave", LinkSource: "manual",
		NowPlaying: &apiNowPlaying{Category: categoryGame, Title: "Hytale"},
	})
	d.at(t, 95*time.Second)
	hytale := d.makeDraft(t, "uid-bob")
	require.Equal(t, &clipTag{Category: categoryGame, Title: "Hytale"}, hytale.Tag)

	// Nothing on: no tag, and nothing said of one.
	d.goLive(apiStream{StreamKey: "videokey-dave", Title: "Dave", UserID: "uid-dave", LinkSource: "manual"})
	d.at(t, 130*time.Second)
	response = d.cut(t, "uid-bob", d.dave, clipSourceLive)
	untagged := decodeDraft(t, response, http.StatusCreated)
	require.Nil(t, untagged.Tag)
	require.NotContains(t, response.Body.String(), `"tag"`)

	response = d.publish(t, "uid-bob", untagged.ID, publishBody("", 0, 10000))
	require.Nil(t, decodeClipView(t, response, http.StatusCreated).Tag)
	require.NotContains(t, response.Body.String(), `"tag"`)
	require.Nil(t, d.stored(t).clip(untagged.ID).Tag)

	// The end of an earlier stretch of it, held after it dropped while
	// Elden Ring was on: a draft cut from that takes Elden Ring, though
	// nothing is on now, and says how the stream stopped.
	ended := newMediaTestPipe(t, draftsClipLength)
	endedStream := newMediaFixtureStream(t, ended)
	endedStream.start = d.now().Add(-50 * time.Second).UnixNano()
	endedStream.play(40 * time.Second)
	tailRing := ended.writer.finish(context.Background(), true)
	require.NotNil(t, tailRing)
	d.rec.setTail(d.dave, clipTail{
		ring:   tailRing,
		until:  d.now().Add(10 * time.Minute),
		reason: clipTailDropped,
		stream: apiStream{
			StreamKey: "videokey-dave", Title: "Dave", UserID: "uid-dave", Username: "dave", LinkSource: "manual",
			NowPlaying: &apiNowPlaying{Category: categoryGame, Title: "ELDEN RING on videokey-dave", Media: eldenRing()},
		},
	})
	response = d.cut(t, "uid-bob", d.dave, clipSourceTail)
	fromEnd := decodeDraft(t, response, http.StatusCreated)
	require.Equal(t, want, fromEnd.Tag)
	require.Equal(t, clipTailDropped, fromEnd.TailReason)
	require.NotContains(t, response.Body.String(), "videokey-dave")
}

// A tag is kept compact — never a title's overview or facts — and checked:
// looked-up media that fails the checks a plan's does, or is not of the
// tag's category, is dropped and the tag keeps its words; a tag with no
// category it knows, or no words, is no tag. A tag written into the document
// by hand is checked again on the way out (A§4.14).
func TestClipTagIsCompactAndChecked(t *testing.T) {
	tag := clipTagOf(&apiNowPlaying{Category: categoryMovie, Title: "Heat", Media: heat()}, "videokey-dave")
	require.Equal(t, &clipTag{Category: categoryMovie, Title: "Heat", Media: compactOf(heat())}, tag)

	encoded, err := json.Marshal(tag)
	require.NoError(t, err)
	require.JSONEq(t, `{"category":"movie","title":"Heat","media":{"ref":"tmdb:movie:949","kind":"movie","title":"Heat","year":"1995","link":"https://www.imdb.com/title/tt0113277/","source":"TMDB"}}`, string(encoded))

	broken := func(change func(media *scheduleMedia)) *scheduleMedia {
		media := eldenRing()
		change(media)

		return media
	}
	for about, media := range map[string]*scheduleMedia{
		"a link anywhere else":  broken(func(m *scheduleMedia) { m.Link = "javascript:alert(1)" }),
		"another source":        broken(func(m *scheduleMedia) { m.Source = mediaSourceTMDB }),
		"a kind its ref is not": broken(func(m *scheduleMedia) { m.Kind = mediaKindMovie }),
		"a ref that is not one": broken(func(m *scheduleMedia) { m.Ref = "steam:../../etc" }),
		"no title":              broken(func(m *scheduleMedia) { m.Title = "  " }),
		"a movie, as a game":    heat(),
	} {
		tag := clipTagOf(&apiNowPlaying{Category: categoryGame, Title: "Something", Media: media}, "videokey-dave")
		require.Equal(t, &clipTag{Category: categoryGame, Title: "Something"}, tag, about)
	}

	// A show is a movie's category, of its own kind.
	tag = clipTagOf(&apiNowPlaying{Category: categoryMovie, Title: "The Bear", Media: theBear()}, "videokey-dave")
	require.Equal(t, compactOf(theBear()), tag.Media)
	require.Equal(t, mediaKindTV, tag.kind())
	require.Equal(t, mediaKindMovie, (&clipTag{Category: categoryMovie, Title: "x"}).kind())
	require.Equal(t, mediaKindGame, (&clipTag{Category: categoryGame, Title: "x"}).kind())

	for about, playing := range map[string]*apiNowPlaying{
		"nothing on":           nil,
		"a category not known": {Category: "podcast", Title: "Chat"},
		"no words":             {Category: categoryMovie, Title: "   "},
	} {
		require.Nil(t, clipTagOf(playing, "videokey-dave"), about)
	}
	require.Equal(t, "the stream", clipTagOf(&apiNowPlaying{Category: categoryMovie, Title: "videokey-dave"}, "videokey-dave").Title)

	// The key comes out before the words are cut, so none of it is left.
	long := clipTagOf(&apiNowPlaying{Category: categoryGame, Title: strings.Repeat("x", 115) + " VideoKey-Dave"}, "videokey-dave")
	require.LessOrEqual(t, len([]rune(long.Title)), maxMediaTitle)
	require.NotContains(t, strings.ToLower(long.Title), "villanet")
	require.NotContains(t, strings.ToLower(long.Title), "vill")

	// On the way out, a tag written by hand into the document.
	w := newClipsWorld(t, "uid-bob", "uid-dave")
	names := w.p.clipNames(clipsNow, true)
	entry := testClip(clipA, "uid-bob", clipsNow.Add(-time.Hour), nil)

	entry.Tag = &clipTag{Category: categoryGame, Title: "ok", Media: &clipTagMedia{Ref: "steam:1", Kind: mediaKindGame, Title: "x", Link: "javascript:alert(1)", Source: mediaSourceSteam}}
	view := clipViewOf(&entry, clipViewer{UserID: "uid-bob"}, retentionRecord{Days: 30}, func(string) bool { return true }, names)
	require.Equal(t, &clipTag{Category: categoryGame, Title: "ok"}, view.Tag)
	require.Equal(t, &clipTag{Category: categoryGame, Title: "ok"}, clipCardOf(&entry, names).Tag)

	entry.Tag = &clipTag{Category: "chat", Title: "hi"}
	view = clipViewOf(&entry, clipViewer{UserID: "uid-bob"}, retentionRecord{Days: 30}, func(string) bool { return true }, names)
	require.Nil(t, view.Tag)
	require.Nil(t, clipCardOf(&entry, names).Tag)
}

// A shared post carries the clip's tag in its props, for its card — the
// title, and the kind a phone or a card can draw an icon for — and never in
// its message, which says nothing anybody typed. Nobody can change it after,
// and a forged card loses it with every other VillaNET prop (A§4.14, A§4.3).
func TestShareCarriesTheTagButNotInTheMessage(t *testing.T) {
	w, srv := newShareWorld(t)
	w.changeClip(t, clipA, func(entry *clipEntry) {
		entry.Tag = &clipTag{Category: categoryGame, Title: "ELDEN RING", Media: compactOf(eldenRing())}
	})

	postID := decodeShare(t, w.share(t, "uid-dan", clipA, townSquare), http.StatusCreated).PostID
	srv.lock.Lock()
	sent := srv.sent[0]
	srv.lock.Unlock()
	require.Equal(t, "ELDEN RING", sent.GetProp(clipPropTag))
	require.Equal(t, mediaKindGame, sent.GetProp(clipPropTagKind))
	for _, word := range []string{"ELDEN", "RING", "game"} {
		require.NotContains(t, sent.Message, word)
	}

	stored := srv.post(postID)
	require.Equal(t, "ELDEN RING", stored.GetProp(clipPropTag))
	require.Equal(t, mediaKindGame, stored.GetProp(clipPropTagKind))
	require.NotContains(t, stored.Message, "ELDEN")

	// Its author cannot change it.
	patched := stored.Clone()
	patched.AddProp(clipPropTag, "something else")
	patched.AddProp(clipPropTagKind, "movie")
	_, appErr := w.p.API.UpdatePost(patched)
	require.Nil(t, appErr)
	require.Equal(t, "ELDEN RING", srv.post(postID).GetProp(clipPropTag))
	require.Equal(t, mediaKindGame, srv.post(postID).GetProp(clipPropTagKind))

	// A movie typed with nothing looked up, and a show, as their kinds.
	w.changeClip(t, clipA, func(entry *clipEntry) {
		entry.Tag = &clipTag{Category: categoryMovie, Title: "Heat"}
	})
	movie := srv.post(decodeShare(t, w.share(t, "uid-dan", clipA, offTopic), http.StatusCreated).PostID)
	require.Equal(t, "Heat", movie.GetProp(clipPropTag))
	require.Equal(t, mediaKindMovie, movie.GetProp(clipPropTagKind))

	w.changeClip(t, clipA, func(entry *clipEntry) {
		entry.Tag = &clipTag{Category: categoryMovie, Title: "The Bear", Media: compactOf(theBear())}
	})
	show := srv.post(decodeShare(t, w.share(t, "uid-dave", clipA, townSquare), http.StatusCreated).PostID)
	require.Equal(t, mediaKindTV, show.GetProp(clipPropTagKind))

	// A clip with no tag says nothing of one.
	w.changeClip(t, clipA, func(entry *clipEntry) { entry.Tag = nil })
	untagged := srv.post(decodeShare(t, w.share(t, "uid-dave", clipA, offTopic), http.StatusCreated).PostID)
	require.NotContains(t, untagged.GetProps(), clipPropTag)
	require.NotContains(t, untagged.GetProps(), clipPropTagKind)

	// A forged card loses it with everything else of VillaNET's.
	forged := &model.Post{UserId: "uid-dan", ChannelId: townSquare, Type: clipPostType, Message: "fake"}
	forged.SetProps(model.StringInterface{clipPropID: clipA, clipPropTag: "pwned", clipPropTagKind: "game"})
	result, _ := posted(t, w.p, forged)
	require.Empty(t, result.GetProp(clipPropTag))
	require.Empty(t, result.GetProp(clipPropTagKind))
}

// A deleted clip's posts lose its tag with its title and its people; an
// expired one's keep it, so the card can still say what it was (A§4.14,
// A§4.5).
func TestDeletedClipScrubsTheTag(t *testing.T) {
	tagged := func(t *testing.T) (*clipsWorld, *shareServer, string) {
		t.Helper()

		w, srv := newShareWorld(t)
		w.changeClip(t, clipA, func(entry *clipEntry) {
			entry.Tag = &clipTag{Category: categoryGame, Title: "ELDEN RING", Media: compactOf(eldenRing())}
		})
		postID := decodeShare(t, w.share(t, "uid-dan", clipA, townSquare), http.StatusCreated).PostID
		require.Equal(t, "ELDEN RING", srv.post(postID).GetProp(clipPropTag))

		return w, srv, postID
	}

	t.Run("deleted by its clipper", func(t *testing.T) {
		w, srv, postID := tagged(t)

		response := w.call(t, "uid-bob", clipCall{method: http.MethodDelete, target: clipsPath + "/clip?id=" + clipA})
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		w.p.clips().background.Wait()

		post := srv.post(postID)
		require.Equal(t, clipMarkDeleted, post.GetProp(clipPropGone))
		for _, key := range []string{"villanet_clip_tag", "villanet_clip_tag_kind"} {
			require.NotContains(t, post.GetProps(), key)
		}
		requireNamesGone(t, post.GetProps())
		require.NotContains(t, fmtProps(post.GetProps()), "ELDEN")
	})

	t.Run("expired", func(t *testing.T) {
		w, srv, postID := tagged(t)

		report := w.p.sweepClipsAt(context.Background(), clipsNow.Add(31*24*time.Hour))
		require.Equal(t, 1, report.Expired)
		w.p.clips().background.Wait()

		post := srv.post(postID)
		require.Equal(t, clipMarkExpired, post.GetProp(clipPropGone))
		require.Equal(t, "ELDEN RING", post.GetProp(clipPropTag))
		require.Equal(t, mediaKindGame, post.GetProp(clipPropTagKind))
	})

	// The mark an edit carries while the marker is at work scrubs it too.
	props := model.StringInterface{clipPropID: clipA, clipPropTitle: "t", clipPropTag: "ELDEN RING", clipPropTagKind: "game"}
	applyGoneMark(props, clipMarkDeleted)
	require.Equal(t, model.StringInterface{clipPropID: clipA, clipPropGone: clipMarkDeleted}, props)
}

func fmtProps(props model.StringInterface) string {
	encoded, _ := json.Marshal(props)

	return string(encoded)
}
