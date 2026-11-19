# Award artwork

The source artwork for the leaderboard awards: a crown for the all-time airtime
board and a calendar for the current month, in gold, silver and bronze; gold
binoculars for the Top Snooper, first place on the all-time Snoopers board; and a
gold clapperboard for the Best Clipper, whose clips have the most upvotes of all
time.

| File | Award |
| --- | --- |
| `stream_alltime_1..3` | Crown: 1st, 2nd, 3rd for airtime, all time |
| `stream_monthly_1..3` | Calendar: 1st, 2nd, 3rd for airtime this month |
| `snoop_alltime_1` | Binoculars: Top Snooper, all time |
| `clip_best` | Clapperboard: Best Clipper, all time |
| `stream_past_1..3` | Calendar with a star: the best place somebody took in a month that has ended (3.2.0) |
| `fan_biggest` | Heart: Biggest fan of a streamer (3.2.0) |
| `ach_<family>_1..6` | Coin: an achievement's step, for the families `streamed`, `snooped`, `podium`, `thumbs` and `channel` (the 24/7 channels' moon) (3.2.0) |

The achievement coins (3.2.0) carry their family as a glyph and their step as
metal: bronze, silver and gold coins for steps 1 to 3; step 4 an ink coin with
the plugin's red as a wide rim, so it never reads as a second On Mumble disc
beside the headphones; step 5 an ink coin with a gold rim and a gold glyph; and
past five, a gold stud on the rim for each step more (step 6 here has one, and
the panel draws up to three). The past-month calendar is the month board's,
with a star where the place digit was, so it is not mistaken for this month's.
The heart, like the binoculars, has one holder per streamer, so one gold file.

These 3.2.0 files were rendered from the components in `badges.tsx` (the
React elements to static SVG, then `rsvg-convert` as below), so for them the
inlined copy is the original and these are the copies.

The binoculars have only the one file. The award has one holder, so there is
no silver or bronze to draw, and no digit in it: there is nothing to tell it
apart from. Otherwise it is drawn exactly as the crown is, on the same 128 by
128 grid: the outline once with a heavy cream stroke (the halo that keeps it
readable on black), then again with a thin dark one over the gold fill, and the
lenses in the same dark ink as the crown's digit.

The clapperboard is the same kind of award and is drawn the same way: one file,
no digit, a gold board under a dark clapper arm, the halo pass and then the ink
pass, then three cream stripes across the arm and a dark play mark on the board.
It means *clips*; the plain line-drawn scissors in the panel are the *action* of
making one, and are not an award.

The panel does **not** load these files. The geometry is inlined as React
components in `webapp/src/components/badges.tsx`, so the badges cost no request,
stay crisp at any size, and work with the plugin's rule that it makes no
external calls. They live here rather than in `assets/`, which is copied
wholesale into the installable bundle: nothing at runtime reads them, so they
would be dead weight in every install. Kept as the original, and because the
PNGs are the right thing to upload if you ever want them as Mattermost custom
emoji.

The PNGs are the SVGs rendered at 128 by 128 on a transparent background:

```bash
rsvg-convert -w 128 -h 128 snoop_alltime_1.svg -o snoop_alltime_1.png
```

A change to a drawing goes in both places, the SVG here and its copy in
`badges.tsx`, and the PNG is rendered again from the SVG.

One deliberate difference in the inlined copies: the artwork asks for
`Liberation Sans` for the rank digit, which is not installed on most of the
machines this runs on and cannot be fetched. The components use a generic
grotesque stack instead, so the digit degrades to a near-identical shape rather
than to a serif.
