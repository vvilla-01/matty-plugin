package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Twitch live changed VillaNET's oldest promise: with either Twitch switch on,
// the server contacts Twitch, and a browser may too (design/twitch-live.md
// §10). 3.4.0 adds two more exceptions, Steam and Hytale, each with a browser
// of its own going there to sign in (design/3.4.0.md §7). With push
// notifications on, the server also posts to the UnifiedPush push servers
// people's phones name (docs/push.md): a fourth. With Look for updates on, the
// server asks GitHub for the plugin's newest release (docs/updates.md): a
// fifth. Every change is to be stated honestly everywhere, so every sentence
// that promises nobody is contacted must name all five, or be on the short
// list below with the reason it stays true as it is. A count of the cases ("in exactly two cases") is such a
// promise too: one that leaves a service out is the same mistake in a number.

// noOutsideContact is the shape of such a promise. "Only outside
// connections" is here because the Mumble setting's help text says it in
// those words, and an early 3.4.0 build left that sentence naming Twitch alone.
// "Contacts besides" is the list in the security chapter, which once left
// GitHub out. "Other outbound connections" is a list of them too, in the
// pop-out link's chapter.
var noOutsideContact = regexp.MustCompile(`(?i)never contact|no outside requests|no external network requests|only outbound connection|only outside connections?|other outbound connections?|talks? only to this server|in exactly \w+ cases|contacts? besides`)

// noOutsideContactExceptions are what every such promise must name.
var noOutsideContactExceptions = []string{"Twitch", "Steam", "Hytale", "UnifiedPush", "GitHub"}

// noOutsideContactExempt are the promises that stay true with Twitch, Steam,
// Hytale, push notifications and plugin updates on, each with why.
var noOutsideContactExempt = map[string]string{
	"Browsers never contact Broadcast Box at all": "about Broadcast Box only, which none of the five changes",
}

// namesEveryException is whether a sentence names Twitch, Steam, Hytale,
// UnifiedPush and GitHub.
// documents is every document a reader is given: the front page, the manual
// (each chapter in docs/, so a chapter added later is read too) and LOCKDOWN.md.
func documents(t *testing.T) []string {
	manual, err := filepath.Glob("../docs/*.md")
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(manual), 9, "the manual's chapters in docs/")
	return append(append([]string{"../README.md"}, manual...), "../LOCKDOWN.md")
}

func namesEveryException(sentence string) bool {
	for _, name := range noOutsideContactExceptions {
		if !strings.Contains(sentence, name) {
			return false
		}
	}

	return true
}

// sentencesOf splits prose into sentences, roughly but well enough: a full
// stop, a question or an exclamation mark, and the space after it.
func sentencesOf(text string) []string {
	flat := strings.Join(strings.Fields(text), " ")

	var sentences []string
	for _, found := range regexp.MustCompile(`[^.!?]+(?:[.!?]+|$)`).FindAllString(flat, -1) {
		if sentence := strings.TrimSpace(found); sentence != "" {
			sentences = append(sentences, sentence)
		}
	}

	return sentences
}

func TestEveryNoOutsideContactPromiseNamesEveryException(t *testing.T) {
	var texts []string

	raw, err := os.ReadFile("../plugin.json")
	require.NoError(t, err)
	var manifest struct {
		SettingsSchema struct {
			Sections []struct {
				Settings []struct {
					HelpText string `json:"help_text"`
				} `json:"settings"`
			} `json:"sections"`
		} `json:"settings_schema"`
	}
	require.NoError(t, json.Unmarshal(raw, &manifest))
	for _, section := range manifest.SettingsSchema.Sections {
		for _, setting := range section.Settings {
			texts = append(texts, setting.HelpText)
		}
	}

	for _, name := range documents(t) {
		document, err := os.ReadFile(name)
		require.NoError(t, err)
		texts = append(texts, string(document))
	}

	for _, text := range texts {
		for _, sentence := range sentencesOf(text) {
			if !noOutsideContact.MatchString(sentence) || namesEveryException(sentence) {
				continue
			}

			exempt := false
			for phrase := range noOutsideContactExempt {
				exempt = exempt || strings.Contains(sentence, phrase)
			}
			require.True(t, exempt, "this promise is not true with Twitch, Steam, Hytale, push notifications and plugin updates on, and does not name all five: %q", sentence)
		}
	}
}

// The pattern is only as good as the wordings it knows, so here are the ones
// the documents have used, the stale Mumble sentence among them.
func TestTheNoOutsideContactPatternKnowsEveryWording(t *testing.T) {
	for _, sentence := range []string{
		"Browsers never contact Broadcast Box at all.",
		"The panel makes no outside requests.",
		"This is the plugin's only outbound connection besides Broadcast Box itself.",
		"Besides Broadcast Box, this, the title lookups and **Show people live on Twitch** are the only outside connections VillaNET makes.",
		"The browser talks only to this server.",
		"A browser leaves this server in exactly four cases.",
		"**What the server contacts besides Broadcast Box**, each only when an admin has switched it on: the Mumble server.",
	} {
		require.True(t, noOutsideContact.MatchString(sentence), "the pattern misses %q", sentence)
	}
}

// The same for the settings page alone, section subtitles included, which the
// test above does not read.
func TestEverySettingPromiseOfNoOutsideContactNamesEveryException(t *testing.T) {
	raw, err := os.ReadFile("../plugin.json")
	require.NoError(t, err)

	var manifest struct {
		SettingsSchema struct {
			Sections []struct {
				Subtitle string `json:"subtitle"`
				Settings []struct {
					HelpText string `json:"help_text"`
				} `json:"settings"`
			} `json:"sections"`
		} `json:"settings_schema"`
	}
	require.NoError(t, json.Unmarshal(raw, &manifest))

	var texts []string
	for _, section := range manifest.SettingsSchema.Sections {
		texts = append(texts, section.Subtitle)
		for _, setting := range section.Settings {
			texts = append(texts, setting.HelpText)
		}
	}

	for _, text := range texts {
		for _, sentence := range sentencesOf(text) {
			if noOutsideContact.MatchString(sentence) {
				require.True(t, namesEveryException(sentence), "this promise does not name Twitch, Steam, Hytale, UnifiedPush and GitHub: %q", sentence)
			}
		}
	}
}

// With the soundboard on, VillaNET's Mumble connection sends audio, and it was
// never true that it received none: Mumble sends it the audio of whoever talks
// where it is parked, which it drops unheard (design/3.2.0.md §10.2). So no
// document may promise that it never receives a byte, the System Console's
// help texts included.
func TestNoDocSaysThePluginNeverReceivesAudio(t *testing.T) {
	for _, name := range append(documents(t), "../plugin.json") {
		document, err := os.ReadFile(name)
		require.NoError(t, err)
		require.NotContains(t, strings.Join(strings.Fields(string(document)), " "), "never receives a byte", name)
	}
}
