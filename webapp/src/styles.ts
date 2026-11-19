import {staticURL} from './manifest';
import {
    BOOT_MS,
    CRT_OFF_MS,
    CRT_ON_DELAY_MS,
    CRT_ON_MS,
    GHOST_MS,
    LEAVE_MS,
    MARQUEE_GAP,
    MARQUEE_REST_PERCENT,
    MINI_FADE_MS,
    RETURN_DELAY_MS,
    SNAP_MS,
} from './motion';

export const STYLE_ELEMENT_ID = 'matty-plugin-styles';

// Every rule is scoped under .vntv, so removing the single <style> element on
// uninitialize reverts the client completely. Nothing here touches Mattermost's
// own message rendering or typography.
const CSS = `
@font-face {
    font-family: 'VNTV Oswald';
    src: url('${staticURL('fonts/oswald-600.woff2')}') format('woff2');
    font-weight: 600;
    font-style: normal;
    font-display: swap;
}

.vntv,
.vntv *,
.vntv *::before,
.vntv *::after {
    box-sizing: border-box;
}

.vntv {
    /* The server's emblem, drawn only where it has one (data-emblem on the
       panel's and the lock's roots). */
    --vntv-mark: url('${staticURL('brand/logo')}');
    --vntv-display: 'VNTV Oswald', 'Oswald', system-ui, sans-serif;

    /* The skin. The panel's colours by what they are for, taken from
       Mattermost's theme: every rule uses these and nothing literal, but for
       what sits on a picture and for medals. LIVE is the theme's
       do-not-disturb red: it is the same red on every stock theme, and
       Mattermost uses it for a state, as LIVE is one. The error words are the
       theme's error text. */
    --vntv-bg: var(--center-channel-bg, #ffffff);
    --vntv-bg-rgb: var(--center-channel-bg-rgb, 255, 255, 255);
    --vntv-fg: var(--center-channel-color, #3f4350);
    --vntv-fg-rgb: var(--center-channel-color-rgb, 63, 67, 80);
    --vntv-accent: var(--button-bg, #1c58d9);
    --vntv-accent-rgb: var(--button-bg-rgb, 28, 88, 217);
    --vntv-on-accent: var(--button-color, #ffffff);
    --vntv-link: var(--link-color, #386fe5);
    --vntv-live: var(--dnd-indicator, #d24b4e);
    --vntv-live-rgb: var(--dnd-indicator-rgb, 210, 75, 78);
    --vntv-danger: var(--error-text, #d24b4e);
    --vntv-danger-rgb: var(--error-text-color-rgb, 210, 75, 78);
    --vntv-focus: var(--sidebar-text-active-border, #5d89ea);

    /* The second press of a two-press destructive action is stock's
       filled danger button. The theme's error colour taken a fifth of the
       way to black, so white words on it reach 4.5:1 on every stock theme:
       the error text itself is a light salmon on Onyx (3.3:1 under white)
       and a text colour, not a fill. */
    --vntv-danger-fill: color-mix(in srgb, var(--vntv-danger) 80%, #000000);
    --vntv-on-danger: #ffffff;

    /* The message box's three steps for a line, the quiet fills and the
       second-rank words, as stock draws them. */
    --vntv-line: rgba(var(--vntv-fg-rgb), 0.16);
    --vntv-line-hover: rgba(var(--vntv-fg-rgb), 0.24);
    --vntv-line-focus: rgba(var(--vntv-fg-rgb), 0.32);
    --vntv-fill: rgba(var(--vntv-fg-rgb), 0.04);
    --vntv-fill-hover: rgba(var(--vntv-fg-rgb), 0.08);
    --vntv-second: rgba(var(--vntv-fg-rgb), 0.72);
    --vntv-icon: rgba(var(--vntv-fg-rgb), 0.64);
    --vntv-icon-hover: rgba(var(--vntv-fg-rgb), 0.8);

    /* The message box's placeholder, as measured on every stock theme
       (bootstrap's grey wins over Mattermost's own rule there). */
    --vntv-placeholder: #999999;

    /* Stock's raised surface: a menu or a popover. */
    --vntv-raised: var(--elevation-3, 0 6px 14px 0 rgba(0, 0, 0, 0.12));

    /* What sits on a picture keeps its own dark scrim and white words on
       every theme. */
    --vntv-scrim: rgba(0, 0, 0, 0.62);
    --vntv-on-picture: #ffffff;

    display: flex;
    flex-direction: column;
    height: 100%;
    background: #000000;
    color: #ffffff;
    overflow: hidden;
}

/* The panel and the lock take the theme's ground and words. */
.vntv.vntv-panel {
    background: var(--vntv-bg);
    color: var(--vntv-fg);

    /* Never taller than the sidebar gives it. A flex item will not
       shrink below its content by default, so a full body pushed the footer
       and the bottom banner out of the sidebar; with this the body, which
       scrolls already, does the scrolling and the footer stays pinned. */
    min-height: 0;
}

/* The watermark: the server's emblem in the bottom corner, most of it outside
   the panel.

   Hung on .vntv-panel, which only the sidebar carries, rather than on .vntv.
   The first attempt used .vntv:not(.vntv-inline) — naming the two places the
   mark must not appear — and measuring the page found a third: the sidebar
   HEADER is a .vntv root too, twenty-eight pixels tall, and it had a
   three-hundred-pixel emblem behind it. Excluding by hand is only ever right
   about the cases somebody remembered; naming the one place it belongs cannot
   go wrong when a fourth appears.

   position + z-index make the panel a stacking context ON PURPOSE, so that
   z-index: -1 below puts the mark above the black background and beneath every
   piece of content. Without the stacking context, -1 would slide behind the
   opaque background and vanish; without a z-index at all, an absolutely
   positioned pseudo-element paints ABOVE the text, which is a logo thrown over
   the panel rather than under it.

   Nothing escapes that is meant to: the leaderboard is a portal onto
   document.body, and the player's fullscreen uses the real Fullscreen API,
   whose top layer ignores stacking contexts entirely.

   Clipped rather than hidden. The emblem hangs 82px past the right edge, and
   overflow: hidden still lets a box be SCROLLED over what it hides — by
   scrollIntoView, by find-in-page, by dragging a text selection to the edge.
   Any of them slid the whole panel 70px sideways under its own header, with
   no scrollbar to put it back. clip cannot be scrolled at all. A browser too
   old for it drops the line and keeps .vntv's hidden. */
.vntv-panel {
    position: relative;
    z-index: 0;
    overflow: clip;
}

.vntv-panel[data-emblem="true"]::after {
    content: '';
    position: absolute;
    z-index: -1;

    /* 400px, up from 340 to take up more of the empty lower half of the
       panel, and hung off the corner by the same share of itself as before,
       so the crop still reads as the same emblem. */
    right: -82px;
    bottom: -78px;
    width: 400px;
    height: 400px;
    background-image: var(--vntv-mark);
    background-repeat: no-repeat;
    background-position: center;
    background-size: contain;

    /* Rendered at 0.03, 0.045, 0.06 and 0.09 side by side on the panel's own
       black to choose this. 0.03 is barely there and 0.09 competes with the
       footer text over it; between the two, 0.06 is the one Victor picked. The
       artwork is white on black, so only its white lines surface — at 0.06 that
       is RGB 15 on RGB 0. */
    opacity: 0.06;
    pointer-events: none;
    mix-blend-mode: screen;
}

/* Most of a narrow sidebar's width is too much emblem. It keeps its corner and
   gives up its size. */
@media (max-width: 340px) {
    .vntv-panel[data-emblem="true"]::after {
        right: -58px;
        bottom: -55px;
        width: 280px;
        height: 280px;
    }
}

/* The panel shell, undone, for the places this renders inside Mattermost's own
   furniture: the profile popover, a post header, the sidebar title.

   flex-direction is the load-bearing line. .vntv is a column, and neither this
   rule nor .vntv-popover used to reset it — so the popover's Live pill and
   Watch button have been stacking vertically rather than sitting side by side,
   which is not what the gap between them was ever for. */
.vntv-inline {
    display: inline-flex;
    flex-direction: row;
    align-items: center;
    height: auto;
    background: transparent;
    overflow: visible;
}

/* ---- Banner ----
   Matched to Mattermost's own channel banner
   (components/channel_banner/style.scss): 24px tall, 2px/24px padding,
   13px/20px text, centred, ellipsised.

   No font-family, no letter-spacing and no text-transform on purpose:
   Mattermost's banner declares none of them, so it inherits 'Open Sans' from
   the body. Declaring Oswald here was what made this bar look foreign next to
   it, and inheriting rather than hardcoding the family means an admin's own
   font override still applies.

   font-weight IS set, to 600. Mattermost's channel banner renders its text as
   Markdown, so a banner configured as **TEXT** arrives as <strong>, and
   Mattermost styles b and strong at 600 rather than the browser default 700.
   Classification text is bold in practice, so this matches it without the
   plugin having to render Markdown.

   The text renders exactly as typed in the Banner text setting. */
.vntv-banner {
    display: flex;
    overflow: hidden;
    min-height: 24px;
    max-height: 24px;
    flex: 0 0 auto;
    align-items: center;
    justify-content: center;
    padding-block: 2px;
    padding-inline: 24px;
    background: var(--vntv-banner-bg, var(--vntv-accent));
    color: var(--vntv-banner-fg, var(--vntv-on-accent));
    font-size: 13px;
    font-weight: 600;
    line-height: 20px;
    text-align: center;
    text-overflow: ellipsis;
    white-space: nowrap;
}

/* ---- Banner motif: the server's own bar, borrowed from the TV page ----
   Sized to a classification banner, which is a custom bar rendered above and
   below the whole client and is about 20px tall. Mattermost's own
   announcement bar is 40px, but that is not the bar this needs to match.
   Its colour and words are the server's own setting; with none set it
   is the theme's accent with the words drawn on the accent (Banner, above). */

/* ---- Wordmark ---- */
.vntv-header {
    flex: 0 0 auto;
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
    padding: 10px 12px;
    border-bottom: 1px solid var(--vntv-line);
}

/* The title as a stock plugin's: a 20px picture and the name in
   Mattermost's own title style (the header's font, size, weight and colour,
   inherited), one colour. */
.vntv-wordmark {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    min-width: 0;
    line-height: inherit;
    user-select: none;
}

.vntv-wordmark-emblem {
    display: block;
    flex: none;
    width: 20px;
    height: 20px;
    border-radius: 4px;
}

.vntv-wordmark-name {
    overflow: hidden;
    font: inherit;
    color: inherit;
    text-overflow: ellipsis;
    white-space: nowrap;
}

@keyframes vntv-pulse {
    0%, 100% { filter: brightness(1); }
    50% { filter: brightness(1.4); }
}


/* ---- Pills ----
   Every second-rank action is stock's tertiary button: the accent's
   words on a 0.08 ground of it, radius 4, the page's own face in sentence
   case, 24px in a dense row (32px where it stands on its own, below). The
   border is kept, transparent, so the few that draw one on a picture keep
   their size. */
.vntv-pill {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: 5px;
    min-height: 24px;
    padding: 3px 10px;
    border-radius: 4px;
    border: 1px solid transparent;
    color: var(--vntv-accent);
    background: rgba(var(--vntv-accent-rgb), 0.08);
    font-family: inherit;
    font-weight: 600;
    font-size: 12px;
    line-height: 16px;
    white-space: nowrap;
}

.vntv-pill-muted {
    color: var(--vntv-second);
    background: var(--vntv-fill-hover);
}

/* Where the action stands on its own (a dialog's foot, a sheet's
   answer, a card's Done), stock's 32px button. */
.vntv-pop-foot .vntv-pill,
.vntv-sched-saved .vntv-pill,
.vntv-tag-status .vntv-pill,
.vntv-tag-foot .vntv-pill,
.vntv-pill.vntv-tryagain {
    min-height: 32px;
    padding: 7px 16px;
}

.vntv-pill.vntv-tryagain[aria-disabled="true"] {
    cursor: progress;
}

button.vntv-pill {
    cursor: pointer;
    transition: background-color 120ms ease;
}

button.vntv-pill:hover {
    background: rgba(var(--vntv-accent-rgb), 0.16);
}

button.vntv-pill:focus-visible {
    outline: none;
    box-shadow: 0 0 0 2px var(--vntv-focus);
}

button.vntv-pill:disabled {
    cursor: default;
    border-color: transparent;
    color: var(--vntv-second);
    background: var(--vntv-fill-hover);
}

/* ---- Body ----
   The scrollbar's room is kept whether or not the body scrolls, so nothing in
   it changes width as its content grows past the panel. A Twitch tile decides
   between Twitch's player and the narrow plate by its width, and the two are
   different heights: with the room coming and going, a classic scrollbar
   wider than the tile's 8px of hysteresis (Firefox's, which Mattermost's
   6px rule does not reach) flipped it between them, reloading Twitch's
   player many times a second. Overlay scrollbars take no room either way. */
.vntv-body {
    flex: 1 1 auto;
    overflow-y: auto;
    scrollbar-gutter: stable;
    padding: 12px;
    display: flex;
    flex-direction: column;
    gap: 12px;
}

/* While there is more under the footer, the body's foot fades out over its
   last 24px instead of being cut off flat through a line of words; scrolled
   to the end, nothing fades and the last line shows whole (Sidebar.tsx). */
.vntv-body[data-more="true"] {
    -webkit-mask-image: linear-gradient(to bottom, #000000 calc(100% - 24px), transparent);
    mask-image: linear-gradient(to bottom, #000000 calc(100% - 24px), transparent);
}

/* Children of the scrolling body must keep their natural height: as flex items
   they would otherwise shrink, and the player clips its own controls away.
   They are also capped and centred, because the sidebar can be popped out into
   its own window and maximised on a very wide monitor. */
.vntv-body > * {
    width: 100%;
    max-width: 1100px;
    flex: 0 0 auto;
    align-self: center;
}

.vntv-section-title {
    text-align: center;
    font-family: var(--vntv-display);
    font-weight: 600;
    font-size: 12px;
    text-transform: uppercase;
    color: var(--vntv-second);
    margin: 0;
}

/* ---- Tile grid ----
   One column in the docked sidebar, two once there is room for two tiles that
   are still worth looking at.

   The width that matters is the PANEL's, not the window's: the sidebar is
   ~350px inside a 2560px monitor, so a viewport media query would hand it the
   wide layout and produce four postage stamps. A container query asks the only
   question that has a useful answer. The @supports block below is the fallback
   for a client too old for container queries — it reaches the same two-column
   result through auto-fit, just with a less exact breakpoint. */
/* The query has to be asked of an ANCESTOR: container-type makes an element a
   container for its descendants, never for itself, so the grid cannot size
   itself against its own inline-size. Hence this wrapper. */
.vntv-stage {
    container-type: inline-size;
}

.vntv-grid {
    position: relative;
    display: grid;
    grid-template-columns: 1fr;
    gap: 8px;

    /* A tile on its way out is lifted out of the grid and slid BEHIND the ones
       still up, at z-index -1. Isolated, so "behind" means behind its
       neighbours, not behind the whole panel and its black background. */
    isolation: isolate;

    /* How tall one tile may get. With more than one tile the cap has to come
       down or two rows do not fit on screen at once, which is the entire point
       of a grid. */
    --vntv-tile-max: 55vh;
}

.vntv-grid[data-count="2"] {
    --vntv-tile-max: 42vh;
}

.vntv-grid[data-count="3"],
.vntv-grid[data-count="4"] {
    --vntv-tile-max: 34vh;
}

/* Load-bearing, not defensive: a grid item defaults to min-width:auto, so one
   long stream title would push its track past its 1fr share and overflow the
   panel sideways. */
.vntv-grid > * {
    min-width: 0;
}

/* The wrapper round each player: what the panel lifts out and glides. It has no
   look of its own. */
.vntv-tile {
    display: block;
}

@container (min-width: 620px) {
    .vntv-grid:not([data-count="1"]) {
        grid-template-columns: 1fr 1fr;
    }
}

@supports not (container-type: inline-size) {
    /* min(300px, 100%) rather than a bare 300px: a track has a floor it cannot
       go below, so in a sidebar narrower than that the grid would overflow and
       the whole panel would scroll sideways. */
    .vntv-grid:not([data-count="1"]) {
        grid-template-columns: repeat(auto-fit, minmax(min(300px, 100%), 1fr));
    }
}

/* ---- Player ---- */
/* No frame. The picture runs to the tile's edge and is one shape, radius
   4px at all four corners (.vntv-screen); the line under it is on the
   panel's own ground. */
.vntv-player {
    position: relative;
    border: 0;
    border-radius: 4px;
    background: transparent;
    overflow: hidden;
}

/* The screen itself: one 16:9 rectangle that holds a picture, or the absence of
   one. The sizing used to live on the <video>; it is here so that a stream, a
   dropped stream and an idle set are all exactly the same shape and the panel
   does not change height when one becomes another. */
.vntv-screen {
    position: relative;
    width: 100%;
    max-width: calc(var(--vntv-tile-max, 55vh) * 16 / 9);
    max-height: var(--vntv-tile-max, 55vh);
    margin: 0 auto;
    aspect-ratio: 16 / 9;
    border-radius: 4px;
    background: #000000;
    overflow: hidden;
}

/* Full screen is the whole display: square. */
.vntv-player:fullscreen .vntv-screen {
    border-radius: 0;
}

/* Analogue snow. A turbulence tile, shifted in whole jumps rather than
   scrolled: static does not drift, it re-randomises.

   The layer is oversized and moved with transform rather than with
   background-position, because transform is composited and background-position
   repaints — this sits behind a video player and must not compete with it for
   the main thread. */
.vntv-snow {
    position: absolute;
    top: -50%;
    left: -50%;
    width: 200%;
    height: 200%;
    background-image: url("data:image/svg+xml,%3Csvg%20xmlns='http://www.w3.org/2000/svg'%20width='160'%20height='160'%3E%3Cfilter%20id='s'%3E%3CfeTurbulence%20type='fractalNoise'%20baseFrequency='0.9'%20numOctaves='2'%20stitchTiles='stitch'/%3E%3CfeColorMatrix%20type='saturate'%20values='0'/%3E%3C/filter%3E%3Crect%20width='160'%20height='160'%20filter='url%28%23s%29'/%3E%3C/svg%3E");
    background-repeat: repeat;
    opacity: 0.14;
    animation: vntv-snow 520ms steps(1) infinite;
    pointer-events: none;
}

.vntv-snow-fast {
    opacity: 0.3;
    animation-duration: 180ms;
}

@keyframes vntv-snow {
    0% { transform: translate3d(0, 0, 0); }
    25% { transform: translate3d(-37px, 22px, 0); }
    50% { transform: translate3d(19px, -44px, 0); }
    75% { transform: translate3d(-51px, -13px, 0); }
    100% { transform: translate3d(28px, 36px, 0); }
}

/* Scanlines, and the corners going dark. Between them they turn a rectangle of
   noise into a picture tube. */
.vntv-scan {
    position: absolute;
    inset: 0;
    background-image:
        repeating-linear-gradient(rgba(0, 0, 0, 0) 0 2px, rgba(0, 0, 0, 0.42) 2px 4px),
        radial-gradient(ellipse at center, rgba(0, 0, 0, 0) 45%, rgba(0, 0, 0, 0.75) 100%);
    pointer-events: none;
}

/* The bright band that rolls down the screen while it is finding the picture.
   This is the whole of the channel-change effect that anybody will name. */
.vntv-roll {
    position: absolute;
    left: 0;
    right: 0;
    height: 22%;
    background: linear-gradient(
        rgba(255, 255, 255, 0) 0%,
        rgba(255, 255, 255, 0.16) 45%,
        rgba(255, 255, 255, 0.28) 55%,
        rgba(255, 255, 255, 0) 100%);
    animation: vntv-roll 900ms linear infinite;
    pointer-events: none;
}

@keyframes vntv-roll {
    0% { transform: translate3d(0, -120%, 0); }
    100% { transform: translate3d(0, 560%, 0); }
}

/* ---- Power ---- */

/* The tube's own light: white, and invisible except while the tube is going on
   or off, when the picture is nothing but a bright line and then a dot. Above
   everything else on the screen, the plate and the tuning burst included. */
.vntv-screen::after {
    content: '';
    position: absolute;
    inset: 0;
    z-index: 4;
    background: #ffffff;
    opacity: 0;
    pointer-events: none;
}

/* Switching on: a point of light, a line across, then the picture opening out
   from it, overshooting a hair, and the white washing out to the picture.
   Delayed so it happens as the panel's boot sweep passes it rather than under
   the part of the panel still dark. */
.vntv-screen[data-power="on"] {
    animation: vntv-crt-on ${CRT_ON_MS}ms cubic-bezier(0.2, 0.75, 0.25, 1) ${CRT_ON_DELAY_MS}ms both;
}

.vntv-screen[data-power="on"]::after {
    animation: vntv-crt-glow-on ${CRT_ON_MS}ms ease-out ${CRT_ON_DELAY_MS}ms both;
}

@keyframes vntv-crt-on {
    0% { transform: scale(0.015, 0.008); }
    30% { transform: scale(1, 0.008); }
    62% { transform: scale(1, 1.03); }
    100% { transform: scale(1, 1); }
}

@keyframes vntv-crt-glow-on {
    0% { opacity: 1; }
    40% { opacity: 0.9; }
    100% { opacity: 0; }
}

/* Switching off: the picture collapses to a bright line, the line draws in to
   a dot, and the dot goes out. Held at the end, because the tile is only taken
   away once this has finished and nothing should come back in between. */
.vntv-screen[data-power="off"] {
    animation: vntv-crt-off ${CRT_OFF_MS}ms cubic-bezier(0.55, 0, 0.85, 0.35) both;
}

.vntv-screen[data-power="off"]::after {
    animation: vntv-crt-glow-off ${CRT_OFF_MS}ms linear both;
}

@keyframes vntv-crt-off {
    0% { transform: scale(1, 1); opacity: 1; }
    45% { transform: scale(1, 0.008); opacity: 1; }
    72% { transform: scale(0.08, 0.008); opacity: 1; }
    88% { transform: scale(0.012, 0.022); opacity: 1; }
    100% { transform: scale(0, 0); opacity: 0; }
}

@keyframes vntv-crt-glow-off {
    0% { opacity: 0; }
    35% { opacity: 0.92; }
    100% { opacity: 1; }
}

/* The tile's ground is the panel's now, so while the tube goes on or
   off the glass it shrinks on stays black behind it, as a set's does, and
   the bright line is always drawn on black. Sized exactly as .vntv-screen
   is, under it in the tile. */
.vntv-player:has(> .vntv-screen[data-power])::before {
    content: '';
    position: absolute;
    top: 0;
    left: 0;
    right: 0;
    width: 100%;
    max-width: calc(var(--vntv-tile-max, 55vh) * 16 / 9);
    max-height: var(--vntv-tile-max, 55vh);
    margin: 0 auto;
    aspect-ratio: 16 / 9;
    border-radius: 4px;
    background: #000000;
}

/* A set that is here because a stream was just switched off comes in quietly.
   Only the one that is here as the panel opens warms up. */
.vntv-player-dead[data-arrive="fade"] {
    animation: vntv-arrive 280ms ease-out both;
}

@keyframes vntv-arrive {
    from { opacity: 0; }
    to { opacity: 1; }
}

/* With other tiles still up: after switching off, the tile is lifted out of the
   grid where it stood and slides up behind them, shrinking and fading, while
   they glide into the space it left. Top, left and size are written on it by
   the panel at the moment it is lifted. */
.vntv-tile[data-leaving="true"] {
    position: absolute;
    z-index: -1;
    pointer-events: none;
    animation: vntv-leave ${LEAVE_MS}ms cubic-bezier(0.5, 0, 0.75, 0) both;
}

@keyframes vntv-leave {
    0% { transform: translateY(0) scale(1); opacity: 1; }
    60% { opacity: 0.45; }
    100% { transform: translateY(-30%) scale(0.9); opacity: 0; }
}

/* And never past the grid's top edge. Closing the TOP tile of two, the tile
   sliding up would otherwise come out from behind and cross the Up next rail
   above — measured at 24px over it, still two-thirds opaque, which is flying
   over other things rather than going behind anything. Only the top edge is
   clipped, and only while a tile is leaving: nothing else in the grid is ever
   above that edge then, because the rail does not change sides until the tile
   has gone. */
.vntv-grid[data-leaving="true"] {
    clip-path: inset(0 -100vw -100vh -100vw);
}

/* ---- Ghost reception ---- */

/* The picture a No Signal screen catches for half a second now and then. Under
   the snow — it is drawn first — so the static stays on top of it the way it
   does on a set that has only half found a channel. */
.vntv-ghost {
    position: absolute;
    top: 50%;
    left: 50%;
    height: 82%;
    aspect-ratio: 1;
    transform: translate(-50%, -50%);
    opacity: 0;
    pointer-events: none;
    user-select: none;
    filter: grayscale(0.3) contrast(1.15) blur(0.5px);
}

/* In jumps, not glides: a signal drifting in and out does not ease. Each step
   holds until the next, and the picture tears sideways as it goes. */
.vntv-screen[data-ghost="true"] .vntv-ghost {
    animation: vntv-ghost ${GHOST_MS}ms steps(1, end) both;
}

/* While the ghost shows, the plate makes way for it and the snow thins.
   Only a server with a ghost ever catches one (NoSignal.tsx): without it
   the plate stays put and the set shows only static. */
.vntv-screen[data-ghost="true"] .vntv-plate {
    opacity: 0;
}

.vntv-screen[data-ghost="true"] .vntv-snow {
    opacity: 0.2;
}

@keyframes vntv-ghost {
    0% { opacity: 0.1; transform: translate(-50%, -50%) translateX(-7px); }
    10% { opacity: 0.32; transform: translate(-50%, -50%) translateX(5px) skewX(-6deg); }
    22% { opacity: 0.16; transform: translate(-50%, -52%) translateX(-2px); }
    34% { opacity: 0.42; transform: translate(-50%, -50%); }
    52% { opacity: 0.28; transform: translate(-50%, -49%) translateX(4px) skewX(3deg); }
    68% { opacity: 0.38; transform: translate(-50%, -50%) translateX(-3px); }
    84% { opacity: 0.14; transform: translate(-50%, -51%) translateX(6px) skewX(-4deg); }
    100% { opacity: 0; transform: translate(-50%, -50%); }
}

/* ---- Boot ---- */

/* Opening the panel: it starts blank, and a scan line sweeps down it, trailing a
   faint glow in the accent, uncovering everything as it goes — with one flicker at the
   start, the way a set that has just had its power applied does.

   An overlay, not a clip on the content: one element moving by transform, so
   the whole panel is revealed by one line rather than each part of it
   separately, and it costs the compositor nothing. It is taller than the panel
   by the height of the glow above the line, so at the start the glow is out of
   sight above the top and the panel is entirely covered. */
.vntv-panel[data-boot="true"]::before {
    content: '';
    position: absolute;
    left: 0;
    right: 0;
    top: -46px;
    height: calc(100% + 46px);
    z-index: 5;
    pointer-events: none;
    /* The cover is the panel's own ground and the line its words, so
       on a dark theme it is the set warming up as before and on a light one
       it is the same sweep, dark on light; the glow is the accent. */
    background: linear-gradient(to bottom,
        rgba(var(--vntv-accent-rgb), 0) 0,
        rgba(var(--vntv-accent-rgb), 0.2) 40px,
        rgba(var(--vntv-fg-rgb), 0.95) 43px,
        rgba(var(--vntv-fg-rgb), 0.95) 45px,
        var(--vntv-bg) 46px);
    animation: vntv-boot ${BOOT_MS}ms cubic-bezier(0.45, 0, 0.25, 1) both;
}

@keyframes vntv-boot {
    0% { transform: translateY(0); opacity: 1; }
    6% { opacity: 0.35; }
    11% { opacity: 1; }
    100% { transform: translateY(100%); opacity: 1; }
}

/* What a television puts on its own screen: boxed, spaced, and in the display
   face rather than the body one. */
.vntv-plate {
    position: absolute;
    top: 50%;
    left: 50%;
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 4px;
    transform: translate(-50%, -50%);
    border: 2px solid rgba(255, 255, 255, 0.5);
    border-radius: 3px;
    padding: 9px 16px;
    background: rgba(0, 0, 0, 0.55);
    text-align: center;
    max-width: 88%;

    /* A set that thinks it has found a signal takes its NO SIGNAL off the
       screen, so while a ghost comes through, this goes. */
    transition: opacity 90ms linear;
}

.vntv-plate-title {
    color: #ffffff;
    font-family: var(--vntv-display);
    font-weight: 600;
    font-size: 15px;
    line-height: 19px;
    letter-spacing: 0.22em;
    text-transform: uppercase;

    /* Indented by the tracking on the last letter, which would otherwise pull
       the word off centre inside its box. */
    text-indent: 0.22em;
}

.vntv-plate-note {
    overflow: hidden;
    max-width: 100%;
    color: rgba(255, 255, 255, 0.72);
    font-size: 11px;
    line-height: 15px;
    text-overflow: ellipsis;
    white-space: nowrap;
}

/* Over the video while it is being found, and gone the instant there is a
   picture. Not on a timer: it ends when the stream arrives, which is the only
   honest thing for it to mean. */
.vntv-tuning {
    position: absolute;
    inset: 0;
    overflow: hidden;
    background: #000000;
}

/* An idle set is a television that is on, not a panel that failed to load. It
   has no controls at all; its name line just says No Signal, dimmed. */
.vntv-player-dead .vntv-player-name {
    color: var(--vntv-second);
}

.vntv-player video {
    display: block;
    width: 100%;
    height: 100%;
    background: #000000;
    object-fit: contain;
}

/* Where the session pool puts a stream's one video element (sessions.ts). No
   box of its own, so the element lays out as a child of the screen, exactly as
   it did when the tile rendered it there itself, and every rule above still
   matches it. */
.vntv-video-slot {
    display: contents;
}

/* The pooled element itself, for anywhere it is shown outside a tile. */
.vntv-video {
    display: block;
    width: 100%;
    height: 100%;
    background: #000000;
    object-fit: contain;
}

/* Going full screen moves an element into the top layer, which changes how it
   is painted and nothing else — it is still a child of the grid, so it keeps
   inheriting the grid's per-tile height cap. Without this, full screen on one
   of four tiles shows the video at 34% of the display. */
.vntv-player:fullscreen {
    --vntv-tile-max: 100vh;

    background: #000000;
    display: flex;
    flex-direction: column;
    justify-content: center;
}

.vntv-player:fullscreen video {
    max-width: 100%;
    max-height: 100%;
    min-height: 0;
    flex: 1 1 auto;
}

/* A watched stream that has momentarily stopped appearing keeps its slot, so
   the grid does not reflow under the viewer every time OBS reconnects. */
/* No frame, but a waiting tile keeps a dashed line so it still
   reads as waiting; an outline, so it takes no room. */
.vntv-player-waiting {
    outline: 1px dashed var(--vntv-line-hover);
    outline-offset: -1px;
}


/* Under the picture: whose stream it is and nothing else — their face, their
   name, and what they are streaming, on one line. The controls that used to
   live here are on the picture now, and only while it is pointed at. */
/* No rule above it: the picture's own edge is the division, and a red hairline
   between the picture and whose it is only cut the tile in two. */
.vntv-player-bar {
    display: flex;
    align-items: center;
    gap: 8px;
    min-height: 38px;
    padding: 6px 10px;
}

.vntv-player-face {
    width: 24px;
    height: 24px;
    font-size: 10px;
}

/* Where a face goes on a set with nobody on it: the ring and nothing in it.
   Two classes, to outrank .vntv-face's own fill, which comes later. */
.vntv-face.vntv-face-empty {
    background: transparent;
    box-shadow: inset 0 0 0 1px var(--vntv-line-focus);
    color: var(--vntv-icon);
}

/* A quiet set in the ring, so it never reads as a face that did
   not load. */
.vntv-face-empty svg {
    display: block;
}

/* The name and the stream title on one baseline, and this row, not each of
   them, centred against the face. Centred one by one, each was centred on its
   own font — 14px Oswald and the 12px body face put their letters at
   different heights in their boxes — and the title sat a couple of pixels
   above the name. Equal line heights do not fix that; a shared baseline does. */
.vntv-player-text {
    display: flex;
    flex: 1 1 auto;
    align-items: baseline;
    gap: 8px;
    min-width: 0;
}

/* The name holds its ground and the stream title gives way — but only so far:
   a very long name still leaves the title some of the line. */
.vntv-player-name {
    flex: 0 1 auto;
    min-width: 0;
    max-width: 55%;
    overflow: hidden;
    color: var(--vntv-fg);
    font-family: var(--vntv-display);
    font-weight: 600;
    font-size: 14px;
    text-overflow: ellipsis;
    white-space: nowrap;
}

/* With no stream title after it, the name has the whole line. */
.vntv-player-name:last-child {
    max-width: none;
}

.vntv-player-note {
    flex: 1 1 0;
    min-width: 0;
    color: var(--vntv-second);
    font-size: 12px;
}

/* ---- What is on ----
   The tag that says what a stream is showing, the kind's icon and the title,
   on the Live now card and on the line under the picture. A button when the
   title is linked to the real movie, show or game, which opens its profile;
   words otherwise. See NowPlaying.tsx.

   Its words carry its baseline and the icon does not: the icon is centred on
   the words, so the tag can sit in a row that lines its items up by their
   letters, as the line under the picture does, and its words land on the
   same line as the name beside them. */
.vntv-onnow {
    display: inline-flex;
    flex: 0 1 auto;
    align-items: baseline;
    gap: 4px;
    box-sizing: border-box;
    min-width: 0;
    max-width: 100%;
    margin: 0;
    padding: 0 6px;
    border: 1px solid rgba(255, 255, 255, 0.16);
    border-radius: 999px;
    background: var(--vntv-scrim);
    color: var(--vntv-on-picture);
    font: inherit;
    font-size: 11px;
    line-height: 16px;
    text-align: left;
    white-space: nowrap;
}

/* The kind's icon in the tag's own words colour, not a red. */
.vntv-onnow-kind {
    display: flex;
    flex: 0 0 auto;
    align-self: center;
    color: inherit;
    opacity: 0.85;
}

.vntv-onnow-kind svg {
    display: block;
    width: 1.1em;
    height: 1.1em;
}

.vntv-onnow-title {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
}

/* Linked to the real thing, on a picture: a brighter neutral edge
   says there is more behind it; no red. Its focus ring is white with a dark
   halo, as it sits on a picture. */
button.vntv-onnow {
    border-color: rgba(255, 255, 255, 0.4);
    cursor: pointer;
    transition: border-color 120ms ease, background-color 120ms ease;
}

button.vntv-onnow:focus-visible {
    outline: none;
    box-shadow: 0 0 0 2px #ffffff, 0 0 0 3px rgba(0, 0, 0, 0.6);
}

button.vntv-onnow[data-open="true"] {
    border-color: rgba(255, 255, 255, 0.72);
    background: rgba(0, 0, 0, 0.82);
}

@media (hover: hover) {
    button.vntv-onnow:hover {
        border-color: rgba(255, 255, 255, 0.72);
        background: rgba(0, 0, 0, 0.82);
    }
}

/* Under the picture: between the name and the stream title, in the row that
   lines them up on one baseline. It gives way before the name does, and
   leaves the title some of the line. */
.vntv-player-onnow {
    max-width: 40%;
    padding: 0 7px;
    font-size: 11px;
    line-height: 18px;
}

/* Under the picture the tag is on the panel, so it takes the skin:
   a quiet fill and a line, the words colour, the accent when linked or open,
   and the stock focus ring. */
.vntv-onnow.vntv-player-onnow {
    border-color: var(--vntv-line);
    background: var(--vntv-fill);
    color: var(--vntv-fg);
}

button.vntv-onnow.vntv-player-onnow {
    border-color: var(--vntv-line-focus);
}

button.vntv-onnow.vntv-player-onnow:focus-visible {
    box-shadow: 0 0 0 2px var(--vntv-focus);
}

button.vntv-onnow.vntv-player-onnow[data-open="true"] {
    border-color: var(--vntv-accent);
    background: rgba(var(--vntv-accent-rgb), 0.08);
    color: var(--vntv-accent);
}

@media (hover: hover) {
    button.vntv-onnow.vntv-player-onnow:hover {
        border-color: var(--vntv-line-focus);
        background: var(--vntv-fill-hover);
    }
}

/* A title too long for the line scrolls like a ticker; see Marquee in Hud.tsx.
   The words are there twice with a gap, and one lap moves them exactly one
   copy and one gap along, so the jump back to the start is invisible. It rests
   at the start of each lap so the beginning can be read. */
.vntv-marquee {
    display: block;
    overflow: hidden;
    white-space: nowrap;
}

.vntv-marquee-track {
    display: inline-flex;
    max-width: 100%;
}

.vntv-marquee-words {
    overflow: hidden;
    text-overflow: ellipsis;
}

.vntv-marquee[data-moving="true"] {
    -webkit-mask-image: linear-gradient(to right, #000000 calc(100% - 18px), transparent 100%);
    mask-image: linear-gradient(to right, #000000 calc(100% - 18px), transparent 100%);
}

.vntv-marquee[data-moving="true"] .vntv-marquee-track {
    gap: ${MARQUEE_GAP}px;
    max-width: none;
    animation: vntv-marquee linear infinite;
    will-change: transform;
}

.vntv-marquee[data-moving="true"] .vntv-marquee-words {
    flex: 0 0 auto;
    overflow: visible;
}

.vntv-marquee[data-paused="true"] .vntv-marquee-track {
    animation-play-state: paused;
}

/* A link in it has keyboard focus: not merely paused wherever it had got to,
   which could leave the link off the edge, but stopped and back at the start;
   Marquee then scrolls the box to bring the link into view. Set by Marquee
   rather than matched with :focus-within, which a mouse press on a link
   matches too: the words then jumped out from under the pointer before the
   button came up, and the click was lost. */
.vntv-marquee[data-keyboard="true"] .vntv-marquee-track {
    animation: none;
}

/* A link in a streamer's title: the line's own colour, underlined, and the
   hot red when pointed at — it has to read as a link in the muted grey of the
   info line and in white alike. Two classes, to outrank Mattermost's own
   .app__body a colours, which would otherwise paint it the theme's link blue. */
.vntv .vntv-link {
    color: inherit;
    text-decoration: underline;
    text-decoration-color: rgba(var(--vntv-fg-rgb), 0.48);
    text-underline-offset: 2px;
    cursor: pointer;
}

/* Clicked, a link keeps focus, and Mattermost colours .app__body a:focus
   (0,2,1); this outranks it, and the two rules below still win by order. */
.vntv .vntv-link:focus {
    color: inherit;
    outline: none;
}

.vntv .vntv-link:hover {
    color: var(--vntv-link);
    text-decoration-color: currentColor;
}

/* From the keyboard, a ring as well as the colour: a colour alone is easy to
   lose in a line of grey. The stock ring. */
.vntv .vntv-link:focus-visible {
    color: var(--vntv-link);
    outline: 2px solid var(--vntv-focus);
    outline-offset: 1px;
    border-radius: 2px;
}

/* A hashtag in a title is a button — it opens the search over the panel
   rather than going anywhere — that reads as one of the title's links: the
   rules above, in the line's own font and colour. A button brings a font,
   padding, a border and a background of its own, all put back here. */
.vntv .vntv-hashtag {
    display: inline;
    margin: 0;
    padding: 0;
    border: 0;
    border-radius: 0;
    background: none;
    font: inherit;
    letter-spacing: inherit;
    line-height: inherit;
    text-align: inherit;
    text-transform: inherit;
    white-space: inherit;
    vertical-align: baseline;
    -webkit-appearance: none;
    appearance: none;
}

/* Pointing holds it still — where there IS pointing. A phone keeps :hover on
   whatever was tapped last, so without the media query a second tap, meant to
   start it again, left it held by a hover nobody was doing. */
@media (hover: hover) {
    .vntv-marquee:hover .vntv-marquee-track {
        animation-play-state: paused;
    }
}

/* Inside its own edge: the fade is a mask, and a mask clips an outline drawn
   outside the box. */
.vntv-marquee:focus-visible {
    outline: 2px solid var(--vntv-focus);
    outline-offset: -2px;
    border-radius: 2px;
}

@keyframes vntv-marquee {
    0%, ${MARQUEE_REST_PERCENT}% { transform: translate3d(0, 0, 0); }
    100% { transform: translate3d(calc(-1 * var(--vntv-marquee-lap)), 0, 0); }
}

/* ---- On the picture ----
   The switch and the controls, over the picture and only while somebody is
   pointing at it; Hud.tsx decides when. Beneath the tube's own light (z-index 4
   on .vntv-screen::after), so switching off washes over them like everything
   else on the glass. */
.vntv-hud {
    position: absolute;
    inset: 0;
    z-index: 3;
}

/* Asleep, a control is invisible AND takes no clicks, so nothing can be
   pressed that cannot be seen. */
.vntv-hud-control {
    opacity: 0;
    pointer-events: none;
    transition: opacity 180ms ease, transform 180ms ease;
}

.vntv-hud[data-awake="true"] .vntv-hud-control {
    opacity: 1;
    pointer-events: auto;
}

/* Reached with the keyboard, they come up for as long as one of them has
   focus. :focus-visible, not :focus: a mouse click leaves focus on a button,
   and the controls should not stay pinned over the picture because of it.
   A rule of its own, because a browser that does not know :has() drops every
   selector in a list that uses it. */
.vntv-hud:has(:focus-visible) .vntv-hud-control {
    opacity: 1;
    pointer-events: auto;
}

/* A paused clip keeps them up with nobody pointing at it (ClipTile.tsx). */
.vntv-hud[data-keep-up="true"] .vntv-hud-control {
    opacity: 1;
    pointer-events: auto;
}

/* The switch. Round, in the middle, and red — it is the one thing on the
   picture that ends something. */
.vntv-hud-power {
    position: absolute;
    top: 50%;
    left: 50%;
    display: flex;
    box-sizing: border-box;
    align-items: center;
    justify-content: center;
    width: 56px;
    height: 56px;
    margin: -28px 0 0 -28px;
    padding: 0;
    border: 2px solid rgba(255, 255, 255, 0.22);
    border-radius: 50%;
    background: rgba(0, 0, 0, 0.6);
    color: #ffffff;
    cursor: pointer;
    transform: scale(0.88);
}

.vntv-hud[data-awake="true"] .vntv-hud-power {
    transform: scale(1);
}

/* On the picture, so white on its dark scrim. Pointed at, it
   lifts as the other controls on the picture do (a lighter wash on the
   scrim): switching a picture off is undone by switching it on, so it is
   not a destructive action and wears no red. Keyboard focus is the white
   ring with a dark halo that every control on video wears. */
.vntv-hud-power:hover,
.vntv-hud-power:focus-visible {
    border-color: rgba(255, 255, 255, 0.5);
    background: linear-gradient(rgba(255, 255, 255, 0.16), rgba(255, 255, 255, 0.16)), rgba(0, 0, 0, 0.72);
    color: #ffffff;
    outline: none;
}

.vntv-hud-power:focus-visible {
    border-color: #ffffff;
    box-shadow: 0 0 0 2px rgba(0, 0, 0, 0.6);
}

/* The words on the screen give the switch the middle while it is up, on the
   plate's own fade — the one it uses to get out of the way of a ghost. */
.vntv-screen:has(> .vntv-hud[data-awake="true"]) .vntv-plate {
    opacity: 0.16;
}

/* The same when the keyboard brought them up, which never sets data-awake.
   Rules of their own, like the reveal itself, so a browser without :has()
   drops only these. */
.vntv-screen:has(> .vntv-hud :focus-visible) .vntv-plate {
    opacity: 0.16;
}

.vntv-hud:has(:focus-visible) .vntv-hud-power {
    transform: scale(1);
}

/* Sound and full screen, along the bottom on a shade that fades up from the
   edge, so white icons read over any picture. */
.vntv-hud-bar {
    position: absolute;
    right: 0;
    bottom: 0;
    left: 0;
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 22px 8px 6px;
    background: linear-gradient(to top, rgba(0, 0, 0, 0.85), rgba(0, 0, 0, 0));
}

.vntv-hud-spacer {
    flex: 1 1 auto;
}

/* The room a button takes, kept empty: on a tile waiting for its stream,
   where full screen and the new-tab link sat, so the scissors stay where
   they were on the live tile. As wide as .vntv-hud-button; the bar's gap
   does the rest. */
.vntv-hud-slot {
    flex: 0 0 30px;
}

.vntv-hud-button {
    display: flex;
    flex: 0 0 auto;
    box-sizing: border-box;
    align-items: center;
    justify-content: center;
    width: 30px;
    height: 30px;
    padding: 0;
    border: none;
    border-radius: 5px;
    background: transparent;
    color: #ffffff;
    cursor: pointer;
    transition: background-color 120ms ease, color 120ms ease;
}

.vntv-hud-button:hover,
.vntv-hud-button:focus-visible {
    background: rgba(255, 255, 255, 0.14);
    color: #ffffff;
    outline: none;
}

/* The stock ring, white with a dark halo on video. */
.vntv-hud-button:focus-visible {
    box-shadow: inset 0 0 0 2px #ffffff, 0 0 0 1px rgba(0, 0, 0, 0.6);
}

.vntv-hud-button svg {
    display: block;
}

/* The scissors (spec 3.4). Greyed while they cannot be pressed, which says
   why in their name and tooltip; aria-disabled rather than disabled, so they
   can still be reached and read. */
.vntv-hud-button[aria-disabled="true"] {
    cursor: default;
    opacity: 0.4;
}

.vntv-hud-button[aria-disabled="true"]:hover {
    background: rgba(255, 255, 255, 0.08);
    color: #ffffff;
}

/* Reached by Tab, greyed scissors still show where focus is: fully there
   while they have it, the glyph kept grey, and a white ring that the 0.4
   opacity would otherwise wash out to nothing over a picture. */
.vntv-hud-button[aria-disabled="true"]:focus-visible {
    opacity: 1;
    background: rgba(255, 255, 255, 0.14);
    color: rgba(255, 255, 255, 0.55);
    box-shadow: inset 0 0 0 2px rgba(255, 255, 255, 0.85);
}

/* Fully there while they pulse, cutting: the pulse is the point. */
.vntv-hud-button.vntv-hud-busy[aria-disabled="true"] {
    opacity: 1;
}

.vntv-hud-busy svg {
    animation: vntv-hud-busy 1.1s ease-in-out infinite;
}

@keyframes vntv-hud-busy {
    0%, 100% { opacity: 1; }
    50% { opacity: 0.3; }
}

/* How much has been recorded, while it is less than a press would take: a
   small tag on the corner of the scissors, in the display face. Only while
   the controls are up, like the button it is on. */
.vntv-hud-bar .vntv-hud-button {
    position: relative;
}

/* Its corner, inside the 6px gap to the next button, and low enough that the
   glyph's lower blade still shows above it. */
.vntv-hud-tag {
    position: absolute;
    right: -3px;
    bottom: -5px;
    padding: 0 3px;
    border-radius: 3px;
    background: var(--vntv-accent);
    color: var(--vntv-on-accent);
    font-family: var(--vntv-display);
    font-size: 9px;
    font-weight: 600;
    letter-spacing: 0.04em;
    line-height: 12px;
    pointer-events: none;
}

/* The tile can take focus, so a click on the picture lands on it and C
   clips it straight after; no ring round the whole tile for that. */
.vntv-player:focus {
    outline: none;
}

/* The pop-out is a link, and Mattermost colours every link in the app with
   .app__body a, a:hover and a:focus (0,1,1 and 0,2,1). The icon is drawn in
   currentColor, so without these it wore the theme's link colour and looked
   stuck in its hover state beside the white buttons. Strictly above those, not
   level with them: level, the winner depends on the order of style elements
   in the page. */
.vntv-hud .vntv-hud-bar a.vntv-hud-button {
    color: #ffffff;
}

.vntv-hud .vntv-hud-bar a.vntv-hud-button:hover,
.vntv-hud .vntv-hud-bar a.vntv-hud-button:focus-visible {
    color: #ffffff;
}

/* On the shade the volume has to be lighter than the panel's own dark red, and
   it is a short slider rather than the whole width: at full width, on a wide
   tile, it was a long red line through the bottom of somebody's picture. */
.vntv-hud .vntv-volume {
    flex: 0 1 120px;
    background: rgba(255, 255, 255, 0.3);
}

/* Full screen, with nothing pointing at the picture: no cursor either. */
.vntv-player:fullscreen .vntv-hud[data-awake="false"] {
    cursor: none;
}

/* Except over a paused clip, whose controls are up. */
.vntv-player:fullscreen .vntv-hud[data-keep-up="true"] {
    cursor: auto;
}

.vntv-volume {
    min-width: 0;
    height: 4px;
    flex: 1 1 auto;
    padding: 0;
    border-radius: 999px;
    appearance: none;
    -webkit-appearance: none;
    background: rgba(255, 255, 255, 0.3);
    cursor: pointer;
}

.vntv-volume::-webkit-slider-thumb {
    width: 12px;
    height: 12px;
    border: none;
    border-radius: 50%;
    appearance: none;
    -webkit-appearance: none;
    background: #ffffff;
    cursor: pointer;
}

.vntv-volume::-moz-range-thumb {
    width: 12px;
    height: 12px;
    border: none;
    border-radius: 50%;
    background: #ffffff;
    cursor: pointer;
}

.vntv-volume:focus {
    outline: none;
}

.vntv-volume:focus-visible {
    outline: 2px solid #ffffff;
    outline-offset: 2px;
    box-shadow: 0 0 0 5px rgba(0, 0, 0, 0.5);
}

/* Under the picture, on the panel: the hairline is the skin's line
   and an error is the theme's error words. */
.vntv-player-status {
    padding: 8px 10px;
    font-size: 12px;
    color: var(--vntv-second);
    border-top: 1px solid var(--vntv-line);
}

.vntv-player-status.vntv-error {
    color: var(--vntv-danger);
}

/* ---- Live now ----
   Each live stream is a card about the size of an Up next card, in a grid
   that fits as many to a row as the panel has room for: a 16:9 picture of
   what is on — the latest shared still, or a 24/7 channel's own picture —
   with whose it is and what they call it along the bottom, and LIVE, how long
   and how many watching along the top. The card's own button lies behind
   everything on it (see LiveCard.tsx for why), so the words sit above it and
   a click on them still watches. */
/* A container too, which the grid's rows are measured against (below). */
.vntv-live-section {
    display: flex;
    flex-direction: column;
    gap: 5px;
    container: vntv-live-list / inline-size;
}

/* The heading line: the eyebrow, on a line of its own. */
.vntv-live-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
    min-width: 0;
}

/* The same eyebrow as Up next, with a count. */
.vntv-live-eyebrow {
    display: flex;
    align-items: center;
    gap: 6px;
    margin: 0;
    color: var(--vntv-second);
    font-family: var(--vntv-display);
    font-weight: 600;
    font-size: 10px;
    letter-spacing: 0.16em;
    text-transform: uppercase;
}

/* The count: a circle for one digit, a pill for two.

   Centred by the grid rather than by text-align. Text that is wider than the
   room it is given starts at the left and runs out of the right, whatever
   text-align says, so a fallback font with wider digits than Oswald's put the
   digit right of centre. A grid centres it either way, and anything too wide
   spills evenly to both sides.

   Its own layer, because of where it sits. The spaced-out words before it
   leave it at a fraction of a pixel, and the browser snaps the red circle to
   a whole pixel but draws the digit where it falls, so the digit sat up to
   half a pixel off to one side, and which side changed with the panel's width
   and the zoom. A layer is placed on a whole pixel with everything in it, so
   the digit moves with its circle. */
.vntv-live-count {
    display: inline-grid;
    place-content: center;
    place-items: center;
    box-sizing: border-box;
    min-width: 16px;
    height: 16px;
    padding: 0 4px;
    border-radius: 999px;
    background: var(--vntv-live);
    color: #ffffff;
    font-size: 10px;
    line-height: 16px;
    letter-spacing: 0;
    white-space: nowrap;
    font-variant-numeric: tabular-nums;
    will-change: transform;
}

/* Cards grow with the panel, and the rows change at set widths rather than
   whenever a third small card would just squeeze in: one card to a row in the
   narrowest panel, two from 344px, and three only from 680px, where a third
   is still a good size. A panel a few pixels either side of 520px used to
   flip between three small cards and two big ones. Three at the widest,
   about 360px each. Without container queries, as many 168px cards as fit. */
.vntv-live-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(168px, 1fr));
    gap: 8px;
    margin: 0;
    padding: 0;
    list-style: none;
}

@container vntv-live-list (min-width: 344px) {
    .vntv-live-grid {
        grid-template-columns: repeat(2, minmax(0, 1fr));
    }
}

@container vntv-live-list (min-width: 680px) {
    .vntv-live-grid {
        grid-template-columns: repeat(3, minmax(0, 1fr));
    }
}

/* Each card is a container, so what is on it can grow with it. */
.vntv-live {
    position: relative;
    min-width: 0;
    container: vntv-live / inline-size;
}

.vntv-live-card {
    position: relative;
    display: block;
    aspect-ratio: 16 / 9;
    overflow: hidden;
    isolation: isolate;
    border: 1px solid var(--vntv-line);
    border-radius: 4px;
    background: #0b0b0c;
    background-clip: padding-box;
    color: var(--vntv-on-picture);
    cursor: pointer;
    transition: border-color 150ms ease, box-shadow 150ms ease;
}

/* Everything inside is laid over the picture, so without aspect-ratio the
   card would have no height at all. */
@supports not (aspect-ratio: 16 / 9) {
    .vntv-live-card {
        padding-top: 56.25%;
    }
}

.vntv-live-hit {
    position: absolute;
    inset: 0;
    z-index: 1;
    margin: 0;
    padding: 0;
    border: none;
    border-radius: inherit;
    background: transparent;
    cursor: pointer;
}

/* The keyboard ring in the theme's focus colour, drawn on the card
   (whose own edge does not clip it) 2px outside its line, so a strip of the
   panel's ground separates the ring from the card and it never reads as the
   old frame on the card's edge. The button itself draws nothing. */
.vntv-live-hit:focus-visible {
    outline: none;
}

.vntv-live-card:has(.vntv-live-hit:focus-visible) {
    outline: 2px solid var(--vntv-focus);
    outline-offset: 2px;
}

/* Where :has is not understood, the ring stays inside the card's
   edge with a dark halo, as before, so focus is never lost. */
@supports not selector(:has(a)) {
    .vntv-live-hit:focus-visible {
        outline: 2px solid var(--vntv-focus);
        outline-offset: -2px;
        box-shadow: inset 0 0 0 3px rgba(0, 0, 0, 0.6);
    }
}

/* The picture, and what it rests on: the streamer's own picture blurred into
   a wash of its colours, under faint scan lines, so a card with no still yet
   still has a look of its own. The still and a 24/7 album cover sit above
   the lines; the shade that keeps the words readable sits above everything. */
.vntv-live-screen {
    position: absolute;
    inset: 0;
    z-index: 0;
    overflow: hidden;
    border-radius: inherit;
    background: #0b0b0c;
}

.vntv-live-blur {
    position: absolute;
    top: -12%;
    left: -12%;
    width: 124%;
    height: 124%;
    object-fit: cover;
    filter: blur(14px) saturate(1.3) brightness(0.5);
}

/* The wash on a card with no picture is the accent at low strength
   over the dark ground; nothing tinted red by name. */
.vntv-live-wash {
    position: absolute;
    inset: 0;
    background: radial-gradient(120% 90% at 30% 20%, rgba(var(--vntv-accent-rgb), 0.28), rgba(var(--vntv-accent-rgb), 0) 70%), #0b0b0c;
}

.vntv-live-screen::before {
    content: '';
    position: absolute;
    inset: 0;
    z-index: 1;
    background: repeating-linear-gradient(to bottom, rgba(255, 255, 255, 0.035) 0, rgba(255, 255, 255, 0.035) 1px, transparent 1px, transparent 3px);
    pointer-events: none;
}

.vntv-live-screen::after {
    content: '';
    position: absolute;
    inset: 0;
    z-index: 3;
    background: linear-gradient(to bottom, rgba(0, 0, 0, 0.55) 0, rgba(0, 0, 0, 0) 32%, rgba(0, 0, 0, 0) 42%, rgba(0, 0, 0, 0.84) 100%);
    pointer-events: none;
}

.vntv-live-album {
    position: absolute;
    z-index: 2;
    top: 10%;
    left: 50%;
    height: 56%;
    aspect-ratio: 1 / 1;
    object-fit: cover;
    border-radius: 6px;
    transform: translateX(-50%);
    box-shadow: 0 6px 18px rgba(0, 0, 0, 0.6), 0 0 0 1px rgba(255, 255, 255, 0.12);
}

/* A still, and the next one fading in over it. */
.vntv-live-layer {
    position: absolute;
    inset: 0;
    z-index: 2;
    width: 100%;
    height: 100%;
    object-fit: cover;
    opacity: 0;
    transition: opacity 420ms ease, transform 300ms ease, filter 300ms ease;
}

.vntv-live-layer[data-shown="true"] {
    opacity: 1;
}

.vntv-live[data-paused="true"] .vntv-live-layer {
    filter: grayscale(0.8) brightness(0.7);
}

/* Along the top: LIVE, how long, how many watching. */
.vntv-live-tags {
    position: absolute;
    z-index: 2;
    top: 6px;
    left: 6px;
    right: 36px;
    display: flex;
    gap: 4px;
    overflow: hidden;
    container-type: inline-size;
}

.vntv-live-tag {
    display: inline-flex;
    flex: 0 0 auto;
    align-items: center;
    gap: 3px;
    height: 16px;
    padding: 0 5px;
    border-radius: 3px;
    background: rgba(0, 0, 0, 0.62);
    color: #ffffff;
    font-family: var(--vntv-display);
    font-weight: 600;
    font-size: 10px;
    line-height: 16px;
    letter-spacing: 0.05em;
    text-transform: uppercase;
    white-space: nowrap;
    font-variant-numeric: tabular-nums;
}

.vntv-live-tag svg {
    display: block;
}

/* How long it has been live gives way first when the tags run short of room
   — on a narrow card with faces and the round button beside them — so the
   faces' "+N" is never the part cut off. It is in the card's label anyway. */
.vntv-live-tag.vntv-live-uptime {
    display: block;
    flex: 0 1 auto;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
}

/* Who is watching, as faces, the way an Up next card shows who has joined:
   overlapping, each ringed in the tag's own dark so they read as separate. */
.vntv-live-tag.vntv-live-watchers {
    gap: 4px;
    padding-left: 3px;
}

.vntv-live-crew {
    display: flex;
    align-items: center;
}

.vntv-face.vntv-live-crew-face {
    width: 13px;
    height: 13px;
    font-size: 6px;
    box-shadow: 0 0 0 1.5px #000000;
}

.vntv-live-crew-face + .vntv-live-crew-face {
    margin-left: -4px;
}

.vntv-live-tag[data-tone="live"] {
    background: var(--vntv-live);
}

/* The card being watched: the LIVE tag's shape with an eye, quiet. It sits
   on the picture, not on the panel, so it is drawn on the picture's own dark
   glass like its neighbours (the time live, the faces), set apart by a faint
   ring: the same on every theme. Never a second fill beside LIVE's red,
   which on a theme whose accent is red would be two reds side by side, and
   never the panel's ground, which on a light theme is a white chip. */
.vntv-live-tag[data-tone="watching"] {
    background: rgba(0, 0, 0, 0.72);
    color: #ffffff;
    box-shadow: inset 0 0 0 1px rgba(255, 255, 255, 0.24);
}

/* On a narrow card the word gives way to the eye alone, before the
   time live is cut (the row is its own container for that, above). */
@container (max-width: 220px) {
    .vntv-live-tag[data-tone="watching"] .vntv-live-tag-word {
        display: none;
    }
}

/* Twice the row's gap between LIVE and WATCHING, so the two tags never
   read as one block. */
.vntv-live-tag[data-tone="live"] + .vntv-live-tag[data-tone="watching"] {
    margin-left: 4px;
}

.vntv-live-tag[data-tone="paused"] {
    background: #3a3a3e;
    color: #dddddd;
}

.vntv-live-tag[data-tone="always"] {
    color: var(--vntv-on-picture);
}

.vntv-live-pulse {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: #ffffff;
    animation: vntv-live-beat 1.6s ease-in-out infinite;
}

@keyframes vntv-live-beat {
    0%, 100% { opacity: 1; }
    50% { opacity: 0.35; }
}

/* Along the bottom: whose, and what they call it. */
.vntv-live-info {
    position: absolute;
    z-index: 2;
    left: 0;
    right: 0;
    bottom: 0;
    display: flex;
    flex-direction: column;
    gap: 1px;
    min-width: 0;
    padding: 0 8px 6px;
}

.vntv-live-who {
    display: flex;
    align-items: center;
    gap: 5px;
    min-width: 0;
}

.vntv-live-face {
    width: 18px;
    height: 18px;
    font-size: 8px;
    box-shadow: 0 0 0 1px rgba(255, 255, 255, 0.35);
}

.vntv-live-name {
    flex: 0 1 auto;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-family: var(--vntv-display);
    font-weight: 600;
    font-size: 13px;
    line-height: 17px;
    text-shadow: 0 1px 2px rgba(0, 0, 0, 0.8);
}

.vntv-live-note,
.vntv-live-links {
    overflow: hidden;
    color: rgba(255, 255, 255, 0.82);
    font-size: 11px;
    line-height: 14px;
    white-space: nowrap;
    text-shadow: 0 1px 2px rgba(0, 0, 0, 0.9);
}

.vntv-live-note {
    display: block;
    text-overflow: ellipsis;
}

/* The title's links, on a line of their own: each one keeps a visible,
   pressable start however long it is, and clip rather than hidden, so
   focusing one cannot scroll the line and leave it shifted afterwards. */
.vntv-live-links {
    display: flex;
    gap: 6px;
    min-width: 0;
    overflow: clip;
}

.vntv-live-links .vntv-link {
    flex: 0 1 auto;
    min-width: 3em;
    overflow: hidden;
    text-overflow: ellipsis;
}

/* A hashtag is one short word, and an address can be any length, so it is
   the address that gives way: "#soulslike" whole beside an address cut
   short, rather than both cut back. Measured, any share of the shrinking at
   all cost a hashtag its last letters. One longer than the whole line still
   ends in an ellipsis. */
.vntv-live-links .vntv-hashtag {
    flex-shrink: 0;
    max-width: 100%;
}

/* What is on, on the card: one more of the tags along the top, on a line of
   its own above the name. Their face, their capitals, their dark and their
   corners, and its left edge on theirs, so it reads as part of the card. In
   a box and type of its own it looked like something stuck on afterwards;
   as plain words like the name's, it was lost on a bright picture, where the
   card's shade is thinnest that high up. The info is anchored to the bottom
   of the picture, so this rises into the picture and never moves the name
   or the title.

   Two pixels shorter than the tags on the smallest card only: with a line
   of links under the title it sits right under them there, and at their
   height it touched them. A title that opens a profile is ringed in red, as
   the 24/7 tag is, rather than given a border: a border's two pixels are
   what made the old tag touch. */
.vntv-live-onnow {
    align-self: flex-start;
    gap: 3px;
    height: 14px;
    margin-left: -2px;
    border-width: 0;
    border-radius: 3px;
    padding: 0 5px;
    font-family: var(--vntv-display);
    font-weight: 600;
    font-size: 10px;
    line-height: 14px;
    letter-spacing: 0.05em;
    text-transform: uppercase;
    text-shadow: none;
}

.vntv-live-onnow[data-media="true"] {
    box-shadow: none;
}

/* With no round button — nothing playing, or this is the only stream that
   is — the tags have the whole top of the card. */
.vntv-live:not([data-add="true"]) .vntv-live-tags {
    right: 6px;
}

/* With one, what is on stops short of its column too: on the smallest card a
   long title rises high enough to run under it. The same 36px from the card's
   edge the tags keep, less the 9 of the card's border and padding. */
.vntv-live[data-add="true"] .vntv-live-onnow {
    max-width: calc(100% - 27px);
}

/* Watch this as well: round, in the top corner, over the picture — only once
   something is playing, and it fades in when it arrives. Up to its own
   opacity, not to 1: a disabled one (four streams already) would otherwise
   flash bright for a moment and then drop to its dimmed state. */
@keyframes vntv-add-arrive {
    from { opacity: 0; }
}

.vntv-live-add {
    animation: vntv-add-arrive 160ms ease-out;
    position: absolute;
    z-index: 4;
    top: 5px;
    right: 5px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 24px;
    height: 24px;
    margin: 0;
    padding: 0;
    border: 1px solid rgba(255, 255, 255, 0.28);
    border-radius: 50%;
    background: rgba(0, 0, 0, 0.6);
    color: #ffffff;
    cursor: pointer;
    transition: background-color 120ms ease, border-color 120ms ease;
}

.vntv-live-add svg {
    display: block;
}

.vntv-live-add:disabled {
    opacity: 0.45;
    cursor: default;
}

.vntv-live-add:focus-visible {
    outline: none;
    box-shadow: 0 0 0 2px #ffffff, 0 0 0 3px rgba(0, 0, 0, 0.6);
}

.vntv-live[data-watching="true"] .vntv-live-add {
    border-color: var(--vntv-accent);
    background: var(--vntv-accent);
    color: var(--vntv-on-accent);
}

/* What is on screen now, ringed. */
.vntv-live[data-watching="true"] .vntv-live-card {
    border-color: var(--vntv-line);
    box-shadow: none;
}

/* A bigger card, bigger words: at the sizes a wide panel gives a card, the
   small-card type reads as an afterthought in a corner of the picture. */
@container vntv-live (min-width: 240px) {
    .vntv-live-tags {
        top: 8px;
        left: 8px;
        right: 44px;
        gap: 5px;
    }

    .vntv-live-tag {
        height: 19px;
        padding: 0 7px;
        font-size: 11px;
        line-height: 19px;
    }

    .vntv-live-pulse {
        width: 7px;
        height: 7px;
    }

    .vntv-face.vntv-live-crew-face {
        width: 15px;
        height: 15px;
        font-size: 7px;
    }

    .vntv-live-info {
        gap: 2px;
        padding: 0 10px 8px;
    }

    .vntv-live-who {
        gap: 6px;
    }

    .vntv-live-face {
        width: 22px;
        height: 22px;
        font-size: 9px;
    }

    .vntv-live-name {
        font-size: 15px;
        line-height: 20px;
    }

    .vntv-live-note,
    .vntv-live-links {
        font-size: 12px;
        line-height: 16px;
    }

    /* The tags' own size here, and from here on: they stop growing at
       this width, so what is on stops with them. */
    .vntv-live-onnow {
        height: 19px;
        padding: 0 7px;
        font-size: 11px;
        line-height: 19px;
    }

    .vntv-live-add {
        top: 7px;
        right: 7px;
        width: 28px;
        height: 28px;
    }

    .vntv-live-add svg {
        width: 15px;
        height: 15px;
    }
}

@container vntv-live (min-width: 330px) {
    .vntv-live-face {
        width: 26px;
        height: 26px;
        font-size: 10px;
    }

    .vntv-live-name {
        font-size: 17px;
        line-height: 22px;
    }

    .vntv-live-note,
    .vntv-live-links {
        font-size: 13px;
        line-height: 17px;
    }
}

/* Only where there is a pointer that hovers: on a touch screen a hover state
   sticks after the tap. */
@media (hover: hover) {
    .vntv-live-card:hover {
        border-color: var(--vntv-line-focus);
    }

    .vntv-live-card:hover .vntv-live-layer {
        transform: scale(1.04);
    }

    .vntv-live-add:hover:not(:disabled) {
        border-color: rgba(255, 255, 255, 0.6);
        background: rgba(0, 0, 0, 0.82);
    }
}

/* A person's picture, round. Shared by the Live Now cards and the Up next
   rail, which set only the size. Initials stand in when there is no picture:
   a stream with no linked account, or an image that would not load. */
.vntv-face {
    display: inline-flex;
    flex: 0 0 auto;
    box-sizing: border-box;
    align-items: center;
    justify-content: center;
    overflow: hidden;
    margin: 0;
    border-radius: 50%;
    background: #1d1d20;
    box-shadow: 0 0 0 1px rgba(255, 255, 255, 0.14);
    object-fit: cover;
    user-select: none;
}

.vntv-face-initials {
    background: linear-gradient(145deg, #4a4d55, #26282d);
    color: #ffffff;
    font-family: var(--vntv-display);
    font-weight: 600;
    letter-spacing: 0.02em;
    line-height: 1;
}

/* The awards used to be 🥇🥈🥉 at 13px, with the monthly one faded to 75% to
   tell the two apart. They are now distinct artwork — a crown for all time, a
   calendar for the month — so neither has to be dimmed to be identifiable, and
   both render at a size somebody can actually read across the room. */
.vntv-medal {
    display: inline-flex;
    flex: 0 0 auto;
    align-items: center;
    line-height: 0;
}

.vntv-medal svg {
    display: block;
}

/* ---- In voice ----
   One row, and no row at all when nobody is in there. With a single voice
   channel there is no tree to draw, so this is a line of names rather than a
   list with headings — and a line of names does not need a popout to open.

   It scrolls sideways rather than wrapping. A row that grew to three lines on a
   busy night would push the players down the panel, which is the one thing the
   top of the panel must not do. */
.vntv-voice {
    display: flex;
    align-items: center;
    gap: 8px;
    overflow: hidden;
    padding: 6px 9px;
    border: 1px solid var(--vntv-line);
    border-left: 3px solid var(--vntv-accent);
    border-radius: 4px;
    background: var(--vntv-bg);
}

.vntv-voice-icon {
    flex: 0 0 auto;
    color: var(--vntv-icon);
    line-height: 0;
}

.vntv-voice-list {
    display: flex;
    gap: 10px;
    overflow-x: auto;
    overscroll-behavior-x: contain;
    margin: 0;
    padding: 0;
    list-style: none;
}

.vntv-voice-list::-webkit-scrollbar {
    height: 0;
}

.vntv-voice-person {
    flex: 0 0 auto;
    color: var(--vntv-fg);
    font-family: var(--vntv-display);
    font-weight: 600;
    font-size: 12px;
    white-space: nowrap;
}

/* Muted is a state worth seeing but not worth shouting: somebody listening in
   is still in the room. */
.vntv-voice-person[data-muted="true"] {
    color: rgba(var(--vntv-fg-rgb), 0.56);
}

.vntv-voice-person[data-me="true"] {
    color: var(--vntv-accent);
}

/* ---- 3.4.0: Playing now ----
   design/3.4.0.md 5.4, Victor's P2 pick B: In voice's twin, directly under
   it and in its frame exactly: a controller, the word Playing (at every
   width), then each person in a Steam game and the game, with more room
   between people than In voice has, since each is a name and a game. Not
   there at all while nobody is in a game (PlayingStrip.tsx). */
.vntv-playing-eyebrow {
    flex: 0 0 auto;
    color: var(--vntv-second);
    font-family: var(--vntv-display);
    font-weight: 600;
    font-size: 10px;
    letter-spacing: 0.16em;
    text-transform: uppercase;
}

/* A swipe settles with a whole person at the start, the gap before them in
   the row's first 16 px, so nothing of the person before shows there.
   PlayingStrip gives the list the room after its last person that lets the
   end settle the same way, and keeps a row left at its start there when who
   is playing changes, which snapping alone would carry along with the
   person it rested on. */
.vntv-playing .vntv-voice-list {
    gap: 16px;
    scroll-snap-type: x proximity;
    scroll-padding-inline-start: 16px;
}

.vntv-playing .vntv-voice-person {
    scroll-snap-align: start;
}

/* More beyond an edge: the row fades out there, as the Trophies card's tabs
   do, rather than cutting a word off. */
.vntv-playing .vntv-voice-list[data-more-end="true"] {
    -webkit-mask-image: linear-gradient(to right, #000000 calc(100% - 28px), transparent);
    mask-image: linear-gradient(to right, #000000 calc(100% - 28px), transparent);
}

.vntv-playing .vntv-voice-list[data-more-start="true"] {
    -webkit-mask-image: linear-gradient(to left, #000000 calc(100% - 28px), transparent);
    mask-image: linear-gradient(to left, #000000 calc(100% - 28px), transparent);
}

.vntv-playing .vntv-voice-list[data-more-start="true"][data-more-end="true"] {
    -webkit-mask-image: linear-gradient(to right, transparent, #000000 28px, #000000 calc(100% - 28px), transparent);
    mask-image: linear-gradient(to right, transparent, #000000 28px, #000000 calc(100% - 28px), transparent);
}

.vntv-playing-game {
    color: var(--vntv-second);
}

/* Closed, the row gives back the panel's gap above it, so an evening nobody
   plays costs the panel nothing at all; under an open In voice it sits
   close, so the two read as a pair. */
.vntv-reveal-playing[data-open="false"] {
    margin-top: -12px;
}

.vntv-reveal[data-open="true"] + .vntv-reveal-playing[data-open="true"] {
    margin-top: -6px;
}

.vntv-reveal-playing[data-armed="true"] {
    transition: grid-template-rows 280ms cubic-bezier(0.22, 1, 0.36, 1), margin-top 280ms cubic-bezier(0.22, 1, 0.36, 1);
}

/* ---- Up next ----
   A rail of cards across the top of the panel, soonest on the left, scrolling
   sideways when there are more than fit.

   Fixed-width cards rather than flexible ones. A flexible card would make the
   rail rearrange itself every time somebody plans a stream, and the whole value
   of the thing is that the left-hand card is always the next one — a position
   worth being able to glance at without reading.

   The row is not there at all while nothing is planned; see Sidebar.tsx. */
.vntv-upnext {
    display: flex;
    flex-direction: column;
    gap: 5px;
}

.vntv-upnext-eyebrow {
    margin: 0;
    color: var(--vntv-second);
    font-family: var(--vntv-display);
    font-weight: 600;
    font-size: 10px;
    letter-spacing: 0.16em;
    text-transform: uppercase;
}

/* The rail and the arrows laid over it. Over it rather than in it, because
   the rail fades its own edges, which is how a card seems to slide under an
   arrow instead of being cut off by it, and anything inside the rail would
   fade with it. */
.vntv-upnext-track {
    position: relative;
}

/* Padded rather than flush: the focus ring and the lead card's glow both need
   room, and a card cut off mid-shadow reads as a rendering fault.

   No scrollbar. The arrows move the rail one plan at a time, and swiping, a
   trackpad and the keyboard still scroll it as they always did.

   No scroll snapping, which was tried and taken out again. A snap container
   re-snaps to the element it was showing whenever its contents change, so a
   stream planned sooner than all the others — inserted at the left, which is
   the one arrival this rail exists to announce — scrolled the rail one card to
   the right to hold the old view still. Free scrolling simply stays at the
   start, and shows it. */
.vntv-upnext-rail {
    display: flex;
    box-sizing: border-box;
    gap: 8px;
    min-height: 89px;
    overflow-x: auto;
    overscroll-behavior-x: contain;
    scroll-padding-inline: 40px 40px;
    scrollbar-width: none;
    margin: 0;
    padding: 3px;
    list-style: none;
}

.vntv-upnext-rail::-webkit-scrollbar {
    display: none;
}

/* The edges fade only where there is more to see. On the left, a shelf: a
   faint trace of the card under the back arrow, and solid from 40px, which is
   exactly where the arrows land a card (STEP_LANDING in UpNext.tsx). The step up is
   steep on purpose — the card before the one that has landed ends 8px short of
   it, and a gentle ramp left that card's edge standing at nearly full
   strength beside the arrow. On the right, a short fade under the forward
   arrow. The stops are variables so either fade can be on alone. */
.vntv-upnext-track:not([data-start]) .vntv-upnext-rail,
.vntv-upnext-track:not([data-end]) .vntv-upnext-rail {
    -webkit-mask-image: linear-gradient(
        to right,
        transparent var(--vntv-rail-clear, 0px),
        var(--vntv-rail-trace, transparent) var(--vntv-rail-step, 0px),
        #000000 var(--vntv-rail-solid, 0px),
        #000000 calc(100% - var(--vntv-rail-tail, 0px)),
        transparent 100%
    );
    mask-image: linear-gradient(
        to right,
        transparent var(--vntv-rail-clear, 0px),
        var(--vntv-rail-trace, transparent) var(--vntv-rail-step, 0px),
        #000000 var(--vntv-rail-solid, 0px),
        #000000 calc(100% - var(--vntv-rail-tail, 0px)),
        transparent 100%
    );
}

.vntv-upnext-track:not([data-start]) .vntv-upnext-rail {
    --vntv-rail-clear: 0px;
    --vntv-rail-trace: rgba(0, 0, 0, 0.1);
    --vntv-rail-step: 30px;
    --vntv-rail-solid: 40px;
}

.vntv-upnext-track:not([data-end]) .vntv-upnext-rail {
    --vntv-rail-tail: 56px;
}

/* The Up next row as one unit, so it moves as one when it changes sides. It
   fades in when it arrives, on either side, rather than appearing there
   between frames: the first plan made brings it in this way too. */
.vntv-upnext-area {
    display: flex;
    flex-direction: column;
    animation: vntv-arrive 280ms ease-out both;
}

/* The arrows: one plan at a time, and only when there is somewhere to go.
   Round and dark so they read as controls over the cards rather than part of
   one. The back arrow sits at the rail's edge, as the forward one does at the
   other. */
.vntv-upnext-arrow {
    position: absolute;
    top: 50%;
    z-index: 2;
    display: flex;
    box-sizing: border-box;
    align-items: center;
    justify-content: center;
    width: 28px;
    height: 28px;
    margin: -14px 0 0;
    padding: 0;
    border: 1px solid var(--vntv-line);
    border-radius: 50%;
    background: var(--vntv-bg);
    box-shadow: var(--elevation-2, 0 4px 6px 0 rgba(0, 0, 0, 0.12));
    color: var(--vntv-icon);
    cursor: pointer;
    animation: vntv-arrive 160ms ease-out both;
    transition: border-color 140ms ease, background-color 140ms ease, color 140ms ease;
}

.vntv-upnext-arrow[data-side="back"] {
    left: 4px;
}

.vntv-upnext-arrow[data-side="forward"] {
    right: 4px;
}

.vntv-upnext-arrow[data-side="back"] svg {
    transform: rotate(180deg);
}

.vntv-upnext-arrow:hover,
.vntv-upnext-arrow:focus-visible {
    border-color: var(--vntv-line-focus);
    background: var(--vntv-bg);
    color: var(--vntv-fg);
    outline: none;
}

.vntv-upnext-arrow:focus-visible {
    box-shadow: 0 0 0 2px var(--vntv-focus);
}

.vntv-upnext-rail:focus-visible {
    outline: 2px solid var(--vntv-focus);
    outline-offset: -2px;
    border-radius: 4px;
}

.vntv-upnext-card {
    position: relative;
    display: flex;
    flex: 0 0 auto;
    flex-direction: column;
    gap: 3px;
    width: 168px;
    padding: 9px 10px;
    border: 1px solid var(--vntv-line);
    border-left: 3px solid var(--vntv-line-focus);
    border-radius: 4px;
    background: var(--vntv-bg);
}

/* The next one up. Given the accent so the left-hand end of the rail reads as
   the answer to "what is on next" without anybody having to compare times. */
.vntv-upnext-card[data-lead="true"] {
    border-left-color: var(--vntv-accent);
}

.vntv-upnext-card[data-live="true"] {
    border-left-color: var(--vntv-accent);
}

/* A card linked to a movie or game wears its art, faded behind the words and
   darkest on the left where the words are — a poster seen through smoked
   glass, not a picture with text printed over it. Its own stacking context,
   so the art can sit at -1: above the card's background, beneath its text. */
.vntv-upnext-card[data-media="true"] {
    isolation: isolate;
    cursor: default;
}

.vntv-upnext-card[data-media="true"]:not([data-live="true"]) {
    cursor: help;
}

.vntv-upnext-art {
    position: absolute;
    inset: 0;
    z-index: -1;
    width: 100%;
    height: 100%;
    border-radius: inherit;
    object-fit: cover;

    /* A tint of the card's ground, not a second picture: capped so the
       card stays within a few percent of the theme's ground on a light
       theme and a dark one, and faded out above the faces and the time, so
       nothing in the art (a title written on it) runs into that line. */
    opacity: 0.16;
    -webkit-mask-image: linear-gradient(to right, rgba(0, 0, 0, 0.15) 0, rgba(0, 0, 0, 0.55) 45%, #000000 100%), linear-gradient(to bottom, #000000 0, #000000 35%, transparent 70%);
    -webkit-mask-composite: source-in;
    mask-image: linear-gradient(to right, rgba(0, 0, 0, 0.15) 0, rgba(0, 0, 0, 0.55) 45%, #000000 100%), linear-gradient(to bottom, #000000 0, #000000 35%, transparent 70%);
    mask-composite: intersect;
    transition: opacity 200ms ease;
    pointer-events: none;
}

.vntv-upnext-card[data-media="true"]:hover .vntv-upnext-art,
.vntv-upnext-card[data-open="true"] .vntv-upnext-art {
    opacity: 0.24;
}

.vntv-upnext-card[data-media="true"]:focus-visible {
    outline: none;
    box-shadow: 0 0 0 2px var(--vntv-focus);
}

/* ---- A title's profile ----
   Drawn on document.body beside the card it belongs to; see MediaProfile.tsx.
   The .vntv root it carries is the panel's — full height — so all of that is
   undone here for a small floating card.

   A stock popover. The skin's ground and words reach this root as
   they reach the panel's: the theme's centre channel, a 1px line, stock's
   popover corners and elevation. */
.vntv.vntv-mediaprofile {
    position: fixed;
    z-index: 1055;
    display: flex;
    flex-direction: column;
    gap: 10px;
    box-sizing: border-box;
    height: auto;
    overflow: hidden;
    padding: 14px;
    border: 1px solid var(--vntv-line);
    border-radius: var(--radius-m, 8px);
    background: var(--vntv-bg);
    color: var(--vntv-fg);
    box-shadow: var(--elevation-4, 0 8px 24px 0 rgba(0, 0, 0, 0.12));
    animation: vntv-profile-in 160ms ease-out both;
}

@keyframes vntv-profile-in {
    from { opacity: 0; transform: translateY(4px) scale(0.98); }
    to { opacity: 1; transform: none; }
}

/* The wide art, blurred and dimmed right down behind everything: the mood of
   the thing, not a second picture of it. Not drawn: a floating card is
   a stock surface, the theme's ground and words; the poster keeps its
   colour inside its own frame. */
.vntv-mediaprofile-backdrop {
    display: none;
    position: absolute;
    inset: 0;
    z-index: 0;
    width: 100%;
    height: 100%;
    object-fit: cover;

    /* A tint, not a second picture: faint enough that the words in
       the theme's own colour read over it on a light ground and a dark one. */
    opacity: 0.12;
    filter: blur(6px) saturate(1.2);
    transform: scale(1.1);
    pointer-events: none;
}

.vntv-mediaprofile > :not(.vntv-mediaprofile-backdrop) {
    position: relative;
    z-index: 1;
}

.vntv-mediaprofile-head {
    display: flex;
    align-items: flex-start;
    gap: 12px;
}

/* A poster for a movie or show, box art for a game — both portrait. */
.vntv-mediaprofile-cover {
    display: block;
    flex: 0 0 auto;
    overflow: hidden;
    width: 84px;
    height: 126px;
    border-radius: 4px;
    background: var(--vntv-fill-hover);
    box-shadow: var(--elevation-2, 0 4px 6px 0 rgba(0, 0, 0, 0.12)), 0 0 0 1px var(--vntv-line);
}

.vntv-mediaprofile-cover img {
    display: block;
    width: 100%;
    height: 100%;
    object-fit: cover;
}

.vntv-mediaprofile-heading {
    display: flex;
    flex: 1 1 auto;
    flex-direction: column;
    gap: 3px;
    min-width: 0;
}

.vntv-mediaprofile-eyebrow {
    margin: 0;
    color: var(--vntv-accent);
    font-family: var(--vntv-display);
    font-weight: 600;
    font-size: 10px;
    letter-spacing: 0.14em;
    text-transform: uppercase;
}

.vntv-mediaprofile-title {
    margin: 0;
    color: var(--vntv-fg);
    font-family: var(--vntv-display);
    font-weight: 600;
    font-size: 19px;
    line-height: 22px;
    overflow-wrap: anywhere;
}

.vntv-mediaprofile-facts {
    margin: 0;
    color: var(--vntv-second);
    font-size: 11px;
    line-height: 15px;
}

.vntv-mediaprofile-context {
    margin: 4px 0 0;
    color: rgba(var(--vntv-fg-rgb), 0.64);
    font-size: 11px;
    line-height: 15px;
}

.vntv-mediaprofile-overview {
    display: -webkit-box;
    margin: 0;
    overflow: hidden;
    -webkit-box-orient: vertical;
    -webkit-line-clamp: 5;
    color: var(--vntv-fg);
    font-size: 12px;
    line-height: 17px;
}

/* Who is in, by name: the host first. */
.vntv-mediaprofile-crew {
    display: flex;
    flex-direction: column;
    gap: 5px;
}

.vntv-mediaprofile-crew-head {
    margin: 0;
    color: var(--vntv-second);
    font-family: var(--vntv-display);
    font-weight: 600;
    font-size: 10px;
    letter-spacing: 0.14em;
    text-transform: uppercase;
}

.vntv-mediaprofile-crew-list {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    margin: 0;
    padding: 0;
    list-style: none;
}

.vntv-mediaprofile-crew-person {
    display: flex;
    align-items: center;
    gap: 5px;
    padding: 2px 8px 2px 2px;
    border-radius: 999px;
    background: var(--vntv-fill-hover);
    color: var(--vntv-fg);
    font-size: 11px;
}

.vntv-mediaprofile-crew-face {
    width: 18px;
    height: 18px;
    font-size: 8px;
}

.vntv-mediaprofile-crew-host {
    color: var(--vntv-accent);
    font-family: var(--vntv-display);
    font-weight: 600;
    font-size: 9px;
    letter-spacing: 0.1em;
    text-transform: uppercase;
}

.vntv-mediaprofile-join {
    flex: 0 0 auto;
}

.vntv-mediaprofile-foot {
    display: flex;
    align-items: center;
    gap: 10px;
    padding-top: 8px;
    border-top: 1px solid var(--vntv-line);
}

.vntv.vntv-mediaprofile a.vntv-mediaprofile-link {
    color: var(--vntv-link);
    font-family: var(--vntv-display);
    font-weight: 600;
    font-size: 12px;
    letter-spacing: 0.04em;
    text-decoration: none;
}

/* Three classes and the element: Mattermost colours .app__body a:focus at
   (0,2,1), and this is portalled into .app__body. */
.vntv.vntv-mediaprofile a.vntv-mediaprofile-link:focus {
    color: var(--vntv-link);
}

.vntv.vntv-mediaprofile a.vntv-mediaprofile-link:hover,
.vntv.vntv-mediaprofile a.vntv-mediaprofile-link:focus-visible {
    color: var(--vntv-link);
    text-decoration: underline;
    outline: none;
}

.vntv.vntv-mediaprofile a.vntv-mediaprofile-link:focus-visible {
    border-radius: 2px;
    box-shadow: 0 0 0 2px var(--vntv-focus);
}

.vntv-mediaprofile-source {
    flex: 1 1 auto;
    color: rgba(var(--vntv-fg-rgb), 0.64);
    font-size: 10px;
}

.vntv.vntv-mediaprofile:focus {
    outline: none;
}

.vntv-mediaprofile-join[aria-disabled="true"] {
    opacity: 0.6;
    cursor: default;
}

.vntv-mediaprofile-watch {
    flex: 0 0 auto;
}

/* Space kept for the cancel button whether or not this card has one, so the
   lede never reflows when somebody's own card is drawn beside another's. */
.vntv-upnext-lede {
    display: flex;
    align-items: center;
    gap: 5px;
    overflow: hidden;
    padding-right: 16px;
    color: var(--vntv-fg);
    font-family: var(--vntv-display);
    font-weight: 600;
    font-size: 11px;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    text-overflow: ellipsis;
    white-space: nowrap;
}

.vntv-upnext-card[data-soon="true"] .vntv-upnext-lede {
    color: var(--vntv-accent);
}

/* A plan that is live says so with the LIVE tag's own shape. */
.vntv-upnext-card[data-live="true"] .vntv-upnext-lede {
    align-self: flex-start;
    height: 16px;
    padding: 0 5px;
    border-radius: 3px;
    background: var(--vntv-live);
    color: #ffffff;
    line-height: 16px;
}

.vntv-upnext-dot {
    flex: 0 0 auto;
    width: 7px;
    height: 7px;
    border-radius: 50%;
    background: currentColor;
    animation: vntv-pulse 2s ease-in-out infinite;
}

/* A row, not a line of text: the category icon holds its width and the words
   give way. The ellipsis therefore has to live on the words themselves — on the
   row it would have cut the icon off instead. */
.vntv-upnext-title {
    display: flex;
    align-items: center;
    gap: 5px;
    overflow: hidden;
    color: var(--vntv-fg);
    font-family: var(--vntv-display);
    font-weight: 600;
    font-size: 14px;
    line-height: 18px;
}

.vntv-upnext-what {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
}

/* The accent red rather than the hot red: this is a label, and the hot red is
   spoken for by the live dot two lines above it. */
.vntv-upnext-kind {
    display: flex;
    flex: 0 0 auto;
    align-items: center;
    color: var(--vntv-icon);
}

.vntv-upnext-card[data-live="true"] .vntv-upnext-kind {
    color: var(--vntv-accent);
}

/* Who and when: their face, then the clock time. The name is the picture's
   alt text and tooltip, so it is still there for anybody who cannot see it. */
.vntv-upnext-who {
    display: flex;
    align-items: center;
    gap: 6px;
    overflow: hidden;
    color: var(--vntv-second);
    font-size: 11px;
    line-height: 15px;
    white-space: nowrap;
}

.vntv-upnext-avatar {
    width: 22px;
    height: 22px;
    font-size: 9px;
}

.vntv-upnext-clock {
    flex: 0 0 auto;
    font-variant-numeric: tabular-nums;
}

/* Everybody else who is in, beside the host's face: a little smaller than the
   host's, each ringed in the card's own dark so the overlap reads as a stack
   rather than a smudge. */
/* Never squeezed and never clipped: UpNext shows only as many faces as the
   line has room for — one and a count when the Join pill shares it — so this
   has no reason to give way. It used to shrink instead, and cut the faces in
   half. */
.vntv-upnext-crew {
    display: flex;
    flex: 0 0 auto;
    align-items: center;
    margin-left: -3px;
}

.vntv-face.vntv-upnext-crew-face {
    width: 18px;
    height: 18px;
    font-size: 8px;
    box-shadow: 0 0 0 2px var(--vntv-bg);
}

.vntv-upnext-crew-face + .vntv-upnext-crew-face,
.vntv-upnext-crew-face + .vntv-upnext-crew-more {
    margin-left: -5px;
}

.vntv-upnext-crew-more {
    display: flex;
    box-sizing: border-box;
    flex: 0 0 auto;
    align-items: center;
    justify-content: center;
    height: 18px;
    min-width: 18px;
    padding: 0 3px;
    border-radius: 999px;

    /* The quiet fill laid on the card's ground, so it is solid: a
       see-through count let the face under it show through ("B(+1"). */
    background: linear-gradient(rgba(var(--vntv-fg-rgb), 0.16), rgba(var(--vntv-fg-rgb), 0.16)), var(--vntv-bg);
    box-shadow: 0 0 0 2px var(--vntv-bg);
    color: var(--vntv-fg);
    font-size: 8px;
    font-weight: 700;
}

/* Join: a small pill at the end of the card's last line, above the layer that
   makes a live card clickable, so it takes its own press. Filled once you are
   in. */
/* Stock's tertiary button, 24px in the card's dense row, on the
   card's own ground so the art behind never shows through it. */
.vntv-upnext-join {
    position: relative;
    z-index: 2;
    flex: 0 0 auto;
    margin-left: auto;
    padding: 4px 10px;
    border: 0;
    border-radius: 4px;
    background: linear-gradient(rgba(var(--vntv-accent-rgb), 0.08), rgba(var(--vntv-accent-rgb), 0.08)), var(--vntv-bg);
    color: var(--vntv-accent);
    cursor: pointer;
    font-family: inherit;
    font-weight: 600;
    font-size: 12px;
    line-height: 16px;
    transition: background-color 120ms ease, color 120ms ease;
}

.vntv-upnext-join:hover,
.vntv-upnext-join:focus-visible {
    background: linear-gradient(rgba(var(--vntv-accent-rgb), 0.16), rgba(var(--vntv-accent-rgb), 0.16)), var(--vntv-bg);
    outline: none;
}

.vntv-upnext-join:focus-visible {
    box-shadow: 0 0 0 2px var(--vntv-focus);
}

.vntv-upnext-join[aria-pressed="true"] {
    background: var(--vntv-accent);
    color: var(--vntv-on-accent);
}

.vntv-upnext-join[aria-disabled="true"] {
    opacity: 0.6;
    cursor: default;
}

/* A transparent layer over the whole card, so a live one can be clicked
   anywhere to open it. Under the cancel button, which comes after it in the
   DOM and therefore paints on top. */
.vntv-upnext-open {
    position: absolute;
    inset: 0;
    padding: 0;
    border: none;
    border-radius: 4px;
    background: transparent;
    cursor: pointer;
}

.vntv-upnext-open:focus-visible {
    outline: 2px solid var(--vntv-focus);
    outline-offset: -2px;
}

.vntv-upnext-card[data-live="true"]:hover {
    border-top-color: var(--vntv-line-focus);
    border-right-color: var(--vntv-line-focus);
    border-bottom-color: var(--vntv-line-focus);
}

.vntv-upnext-remove {
    position: absolute;
    z-index: 1;
    top: 5px;
    right: 5px;
    display: flex;
    align-items: center;
    justify-content: center;
    width: 18px;
    height: 18px;
    padding: 0;
    border: none;
    border-radius: 4px;
    background: transparent;
    color: var(--vntv-icon);
    cursor: pointer;
    opacity: 0;
    transition: opacity 120ms ease, color 120ms ease;
}

.vntv-upnext-card:hover .vntv-upnext-remove,
.vntv-upnext-remove:focus-visible {
    opacity: 1;
}

.vntv-upnext-remove:hover {
    color: var(--vntv-danger);
}

.vntv-upnext-remove:focus-visible,
.vntv-upnext-edit:focus-visible {
    outline: none;
    box-shadow: 0 0 0 2px var(--vntv-focus);
}

.vntv-upnext-remove svg {
    width: 11px;
    height: 11px;
}

/* The pencil on your own plans, beside the ×, revealed the same way. */
.vntv-upnext-edit {
    position: absolute;
    z-index: 1;
    top: 5px;
    right: 25px;
    display: flex;
    align-items: center;
    justify-content: center;
    width: 18px;
    height: 18px;
    padding: 0;
    border: none;
    border-radius: 4px;
    background: transparent;
    color: var(--vntv-icon);
    cursor: pointer;
    opacity: 0;
    transition: opacity 120ms ease, color 120ms ease;
}

.vntv-upnext-card:hover .vntv-upnext-edit,
.vntv-upnext-card:focus-within .vntv-upnext-edit,
.vntv-upnext-edit:focus-visible {
    opacity: 1;
}

.vntv-upnext-edit:hover {
    color: var(--vntv-fg);
}

.vntv-upnext-edit svg {
    width: 12px;
    height: 12px;
}

/* Room in the lede for the pencil as well as the ×. */
.vntv-upnext-card[data-own="true"] .vntv-upnext-lede {
    padding-right: 36px;
}

/* The share glyph on every listed plan for a linked reader (design/3.2.0.md
   6.6), left of the pencil and the ×, revealed the same way; red once the
   first press has armed it, and shown while a share is under way. */
.vntv-upnext-share {
    position: absolute;
    z-index: 1;
    top: 5px;
    right: 5px;
    display: flex;
    align-items: center;
    justify-content: center;
    width: 18px;
    height: 18px;
    padding: 0;
    border: none;
    border-radius: 4px;
    background: transparent;
    color: var(--vntv-icon);
    cursor: pointer;
    opacity: 0;
    transition: opacity 120ms ease, color 120ms ease, background-color 120ms ease;
}

.vntv-upnext-share[data-slot="1"] {
    right: 25px;
}

.vntv-upnext-share[data-slot="2"] {
    right: 45px;
}

.vntv-upnext-card:hover .vntv-upnext-share,
.vntv-upnext-card:focus-within .vntv-upnext-share,
.vntv-upnext-share:focus-visible,
.vntv-upnext-share[data-phase] {
    opacity: 1;
}

.vntv-upnext-share:hover {
    color: var(--vntv-fg);
}

.vntv-upnext-share:focus-visible {
    outline: none;
    box-shadow: 0 0 0 2px var(--vntv-focus);
}

.vntv-upnext-share[data-phase="armed"] {
    background: var(--vntv-accent);
    color: var(--vntv-on-accent);
}

.vntv-upnext-share[data-phase="sharing"] {
    color: var(--vntv-fg);
}

.vntv-upnext-share[data-phase="shared"] {
    color: var(--vntv-accent);
}

.vntv-upnext-share svg {
    width: 12px;
    height: 12px;
}

/* Room in the lede for two tools, or three. */
.vntv-upnext-card[data-tools="2"] .vntv-upnext-lede {
    padding-right: 36px;
}

.vntv-upnext-card[data-tools="3"] .vntv-upnext-lede {
    padding-right: 56px;
}

/* A live plan's LIVE tag keeps the tag's own padding whatever
   tools the card has (the rules above would have stretched it by their
   room), and keeps clear of the tools by its width instead. */
.vntv-upnext-card[data-live="true"][data-live] .vntv-upnext-lede {
    max-width: calc(100% - 56px);
    padding-right: 5px;
}

/* The card a share is being made from, marked while it is. */
.vntv-upnext-card[data-sharing="true"] {
    border-color: var(--vntv-accent);
}

/* The share's one line, under the rail: where it will go, that it went, or
   why not. No room while it says nothing. */
.vntv-upnext-shared {
    margin: 6px 2px 0;
    color: var(--vntv-second);
    font-size: 12px;
    line-height: 16px;
}

.vntv-upnext-shared:empty {
    margin: 0;
}

/* Where the share will go, after its first press: plain words, so
   the theme's link colour (brighter than the accent on a dark theme), not
   the accent, which is for fills and button words. */
.vntv-upnext-shared[data-tone="armed"] {
    color: var(--vntv-link);
}

.vntv-upnext-shared[data-tone="error"] {
    color: var(--vntv-danger);
}

.vntv-upnext-shared[data-tone="busy"] {
    color: var(--vntv-second);
}

/* Nothing reveals itself on hover where there is no hover. */
@media (hover: none) {
    .vntv-upnext-remove { opacity: 1; }
    .vntv-upnext-edit { opacity: 1; }
    .vntv-upnext-share { opacity: 1; }
}

/* ---- The sidebar header ----
   This is the node handed to registerRightHandSidebarComponent as the panel's
   title, and Mattermost drops it straight into .sidebar--right__title with no
   wrapper. That span is itself a flex container that grows to fill the header,
   so growing to fill IT and spacing the children apart is what puts the trophy
   at the right-hand end, hard against Mattermost's own expand and close
   buttons. No absolute positioning anywhere.

   The resets matter: .vntv is a full-height black flex COLUMN for the panel,
   and the header hands down nowrap white-space and a 16px Metropolis 600.

   And no backticks in here. This stylesheet is a JavaScript template literal,
   where a backtick ends it and the compiler blames the next forty lines. That
   has now cost three separate sessions.

   The 4px above and below are room, not looks. .sidebar--right__title hides
   its overflow and is exactly as tall as this node, so with a 28px row the
   clapper's draft count (4px above its button) lost its top and the focus
   rings (3px outside theirs) lost their top and bottom strokes. 36px still
   sits centred in Mattermost's 56px header, so nothing else moves. */
.vntv-rhs-title {
    display: flex;
    flex: 1 1 auto;
    flex-direction: row;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
    min-width: 0;
    height: auto;
    padding: 4px 0;
    background: transparent;
    color: inherit;
    overflow: visible;
}

/* Mattermost's own header buttons (.btn-icon.btn-sm): 32px square,
   radius 4px, the glyph at the icon opacity, a quiet ground when pointed at,
   and the stock focus ring. */
.vntv-trophy {
    display: inline-flex;
    flex: 0 0 auto;
    align-items: center;
    justify-content: center;
    width: 32px;
    height: 32px;
    padding: 0;
    border: 0;
    border-radius: 4px;
    background: transparent;
    color: var(--vntv-icon);
    cursor: pointer;
    font: inherit;
    line-height: 0;
    transition: background-color 120ms ease, color 120ms ease;
}

.vntv-trophy:hover {
    background: var(--vntv-fill-hover);
    color: rgba(var(--vntv-fg-rgb), 0.8);
}

.vntv-trophy[aria-expanded="true"] {
    background: rgba(var(--vntv-accent-rgb), 0.08);
    color: var(--vntv-accent);
}

.vntv-trophy:focus-visible {
    outline: none;
    box-shadow: 0 0 0 2px var(--vntv-focus);
}

/* The clapper and the trophy, one group at the right-hand end of the header:
   the title spaces its children apart, and a third child of its own would sit
   in the middle of it. */
.vntv-rhs-actions {
    display: inline-flex;
    flex: 0 0 auto;
    align-items: center;
    gap: 2px;
}

/* The clapper is drawn as the trophy is, with room on its corner for the
   reader's draft count and the dot for clips they have not seen. */
.vntv-clapper {
    position: relative;
}

.vntv-clips-count {
    position: absolute;
    top: -4px;
    right: -5px;
    display: inline-grid;
    place-items: center;
    box-sizing: border-box;
    min-width: 15px;
    height: 15px;
    padding: 0 4px;
    border-radius: 999px;
    background: var(--vntv-accent);
    color: var(--vntv-on-accent);
    font-family: var(--vntv-display);
    font-size: 9px;
    font-weight: 600;
    line-height: 15px;
    letter-spacing: 0;
    font-variant-numeric: tabular-nums;
    pointer-events: none;
}

/* The accent dot in a ring of the header's own ground, as
   Mattermost draws an unread dot. */
.vntv-clips-new {
    position: absolute;
    top: 3px;
    right: 3px;
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background: var(--vntv-accent);
    box-shadow: 0 0 0 2px var(--vntv-bg);
    pointer-events: none;
}

/* A count of drafts and the dot together: the dot steps out of its way. */
.vntv-clips-count + .vntv-clips-new {
    top: auto;
    right: -2px;
    bottom: 1px;
}

/* ---- The leaderboard ----
   A portal onto document.body. Both .sidebar--right__header and
   .sidebar--right__title both hide their overflow, so anything opened in place
   from the header is clipped to a strip — and the boards are the one thing in
   this plugin that is genuinely easier to read wide than in a 400px column.

   The z-index clears Mattermost's own modal layer (1050).

   A stock dialog. The page behind dimmed as stock dims it, the card
   on the theme's ground with its words, a 1px line, the dialog's 12px corner
   and stock's highest elevation. The skin's tokens are on every .vntv root,
   so the card only has to take the ground and the words. */
.vntv-modal {
    position: fixed;
    display: flex;
    align-items: center;
    justify-content: center;
    inset: 0;
    z-index: 1060;
    height: auto;
    padding: 24px 16px;
    background: rgba(0, 0, 0, 0.64);
    overflow: auto;
}

.vntv-modal-card {
    display: flex;
    overflow: hidden;
    flex-direction: column;
    width: 100%;
    max-width: 680px;
    max-height: 100%;
    border: 1px solid var(--vntv-line);
    border-radius: 12px;
    background: var(--vntv-bg);
    color: var(--vntv-fg);
    box-shadow: var(--elevation-6, 0 20px 32px 0 rgba(0, 0, 0, 0.12));
}

.vntv-modal-head {
    display: flex;
    align-items: center;
    gap: 10px;
    flex: 0 0 auto;
    padding: 14px 16px;
    border-bottom: 1px solid var(--vntv-line);
}

.vntv-modal-icon {
    display: inline-flex;
    color: var(--vntv-accent);
    line-height: 0;
}

.vntv-modal-title {
    flex: 1 1 auto;
    margin: 0;
    color: var(--vntv-fg);
    font-family: var(--vntv-display);
    font-size: 17px;
    font-weight: 600;
    letter-spacing: 0.02em;
    text-transform: uppercase;
}

/* Stock's 32px icon button. */
.vntv-modal-close {
    display: inline-flex;
    flex: 0 0 auto;
    align-items: center;
    justify-content: center;
    width: 32px;
    height: 32px;
    margin: -2px 0;
    padding: 0;
    border: 0;
    border-radius: 4px;
    background: transparent;
    color: var(--vntv-icon);
    cursor: pointer;
    line-height: 0;
}

.vntv-modal-close:hover {
    background: var(--vntv-fill-hover);
    color: var(--vntv-icon-hover);
}

.vntv-modal-close:focus-visible {
    outline: none;
    box-shadow: 0 0 0 2px var(--vntv-focus);
}

.vntv-modal-body {
    flex: 1 1 auto;
    overflow-y: auto;
    padding: 16px;
}

/* Side by side when there is room for two readable columns, stacked when there
   is not. auto-fit rather than a media query, because this can be opened from a
   popped-out sidebar in its own window. */
.vntv-lb-boards {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
    gap: 22px;
}

/* A long display name must not widen its grid column and squash the other. */
.vntv-lb-board {
    min-width: 0;
}

.vntv-lb-board-head {
    margin-bottom: 10px;
}

.vntv-lb-board-heading {
    margin: 0;
    color: var(--vntv-fg);
    font-family: var(--vntv-display);
    font-size: 14px;
    font-weight: 600;
    letter-spacing: 0.04em;
    text-transform: uppercase;
}

.vntv-lb-board-sub {
    margin: 2px 0 0;
    color: var(--vntv-second);
    font-size: 11px;
}

.vntv-lb-list {
    display: flex;
    flex-direction: column;
    gap: 4px;
    margin: 0;
    padding: 0;
    list-style: none;
}

.vntv-lb-row {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 7px 9px;
    border: 1px solid transparent;
    border-radius: 4px;
}

/* Your own row in the accent, quietly, as stock marks a chosen
   item. */
.vntv-lb-row[data-me="true"] {
    border-color: rgba(var(--vntv-accent-rgb), 0.24);
    background: rgba(var(--vntv-accent-rgb), 0.08);
}

/* A fixed column whether the place is artwork or a number, so every name in
   the list starts at the same x. */
.vntv-lb-place {
    display: inline-flex;
    flex: 0 0 auto;
    align-items: center;
    justify-content: center;
    width: 32px;
    color: var(--vntv-second);
    font-family: var(--vntv-display);
    font-size: 13px;
    font-weight: 600;
    line-height: 0;
}

.vntv-lb-main {
    display: flex;
    flex: 1 1 auto;
    flex-direction: column;
    gap: 5px;
    min-width: 0;
}

.vntv-lb-line {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 10px;
}

.vntv-lb-name {
    overflow: hidden;
    flex: 0 1 auto;
    min-width: 0;
    color: var(--vntv-fg);
    font-family: var(--vntv-display);
    font-size: 14px;
    font-weight: 600;
    text-overflow: ellipsis;
    white-space: nowrap;
}

.vntv-lb-hours {
    flex: 0 0 auto;
    color: var(--vntv-fg);
    font-size: 12px;
    font-variant-numeric: tabular-nums;
}

/* Measured against the leader rather than the total: against the total, eight
   streamers give eight slivers and the chart says nothing. */
.vntv-lb-bar {
    display: block;
    overflow: hidden;
    height: 5px;
    border-radius: 999px;
    background: var(--vntv-fill-hover);
}

/* Progress in the accent. */
.vntv-lb-fill {
    display: block;
    height: 100%;
    border-radius: 999px;
    background: var(--vntv-accent);
}

.vntv-lb-more {
    margin: 8px 0 0;
    color: var(--vntv-second);
    font-size: 11px;
}

/* The monthly medals go under both boards and across the whole card: a tally
   is one short line per person, which reads as a table, not as a third narrow
   column. A board with no other beside it spans the card too. It used to get
   that for free, because auto-fit drops an empty column; with the medals
   holding both columns open, it would be squeezed into half the card. */
.vntv-lb-wide {
    grid-column: 1 / -1;
}

/* Gold, silver and bronze in three fixed columns, so they line up down the
   list whatever each person has. Pushed to the right like the hours on a
   board: the name gives way first, and these never wrap. */
.vntv-lb-tallies {
    display: grid;
    flex: 0 0 auto;
    grid-template-columns: repeat(3, 50px);
    column-gap: 6px;
    margin-left: auto;
}

.vntv-lb-tally {
    display: inline-flex;
    align-items: center;
    gap: 3px;
    min-width: 0;
    color: var(--vntv-fg);
    font-size: 12px;
    font-variant-numeric: tabular-nums;
    white-space: nowrap;
}

.vntv-lb-tally svg {
    display: block;
    flex: 0 0 auto;
}

/* A place never taken: a dash where the badge would be, so the column stays
   a column rather than a gap. */
.vntv-lb-tally[data-none="true"] {
    justify-content: center;
    width: 22px;
    color: var(--vntv-second);
}

/* Wins stand out by weight. No longer in the medal's gold, which
   the card's dark ground used to carry: on a light theme a gold figure is
   about 1.6:1 and cannot be read. The gold medal beside it says first
   place; the figure is in the card's words, at the size of the other
   counts, so a column of wins reads as a column and not as a row of
   buttons. */
.vntv-lb-tally[data-rank="1"] .vntv-lb-tally-count {
    font-weight: 700;
}

.vntv-lb-empty {
    margin: 0;
    padding: 26px 0;
    color: var(--vntv-second);
    font-size: 13px;
    text-align: center;
}

/* What the Watchers' boards count, said once under them, small: they only
   ever list people with a linked stream, and somebody who has watched all
   week without one should be able to find out why they are not there. */
.vntv-lb-note {
    margin: 18px 0 0;
    color: var(--vntv-second);
    font-size: 11px;
    line-height: 1.45;
}

/* ---- The Trophies card's tabs ----
   Streamers and Watchers, on a row of their own under the card's head, so the
   title keeps its place and the close button its corner. Each tab is as wide
   as its word, which leaves room on the row for a third (the Clippers) even
   on the narrowest card. With tabs, the head gives its rule to the row, and
   the chosen tab's underline sits on that rule, so the two read as one line
   with a red stretch in it. */
.vntv-modal-head[data-tabbed="true"] {
    padding-bottom: 6px;
    border-bottom: 0;
}

/* And the card is hung from the top rather than centred. The tabs are not
   the same height as each other, and a centred card moved its own tab row
   out from under the pointer on every switch. justify-content, because a
   .vntv root is a column. */
.vntv-modal[data-tabbed="true"] {
    justify-content: flex-start;
    padding-top: max(24px, 8vh);
}

/* With Achievements and You (3.2.0) the five tabs are 422px, wider than a
   phone's card, so the row scrolls sideways: no scrollbar, and a soft fade
   at whichever end has more tabs beyond it (LeaderboardModal sets the two
   data attributes). The rule under the row is drawn inside it, as a shadow,
   so the scrolling box does not clip the chosen tab's underline.
   scroll-padding is the fade's width and a little more: the chosen tab is
   brought into view clear of the fade, and the last tab reaches the true
   end, which turns that end's fade off. */
.vntv-trophy-tabs {
    display: flex;
    flex: 0 0 auto;
    gap: 2px;
    padding: 0 8px;
    box-shadow: inset 0 -1px 0 var(--vntv-line);
    overflow-x: auto;
    overflow-y: hidden;
    scroll-padding-inline: 40px;
    scrollbar-width: none;
    overscroll-behavior-x: contain;
}

.vntv-trophy-tabs::-webkit-scrollbar {
    display: none;
}

.vntv-trophy-tabs[data-more-end="true"] {
    -webkit-mask-image: linear-gradient(to right, #000000 calc(100% - 36px), transparent);
    mask-image: linear-gradient(to right, #000000 calc(100% - 36px), transparent);
}

.vntv-trophy-tabs[data-more-start="true"] {
    -webkit-mask-image: linear-gradient(to left, #000000 calc(100% - 36px), transparent);
    mask-image: linear-gradient(to left, #000000 calc(100% - 36px), transparent);
}

.vntv-trophy-tabs[data-more-start="true"][data-more-end="true"] {
    -webkit-mask-image: linear-gradient(to right, transparent, #000000 36px, #000000 calc(100% - 36px), transparent);
    mask-image: linear-gradient(to right, transparent, #000000 36px, #000000 calc(100% - 36px), transparent);
}

.vntv-trophy-tab {
    position: relative;
    flex: 0 0 auto;
    margin: 0;
    padding: 9px 10px 10px;
    border: 0;
    border-radius: 4px 4px 0 0;
    background: transparent;
    color: var(--vntv-second);
    cursor: pointer;
    font-family: var(--vntv-display);
    font-size: 13px;
    font-weight: 600;
    letter-spacing: 0.06em;
    line-height: 1.2;
    text-transform: uppercase;
    transition: color 120ms ease, background-color 120ms ease;
}

.vntv-trophy-tab:hover {
    background: var(--vntv-fill-hover);
    color: var(--vntv-fg);
}

/* The chosen tab in the accent, its word and its underline, as
   stock's own tabs are. */
.vntv-trophy-tab[aria-selected="true"] {
    color: var(--vntv-accent);
}

.vntv-trophy-tab[aria-selected="true"]::after {
    content: '';
    position: absolute;
    right: 8px;
    bottom: 0;
    left: 8px;
    height: 2px;
    border-radius: 2px;
    background: var(--vntv-accent);
}

/* Inside the row's scrolling box a shadow would be cut, so the ring is a
   2px outline drawn inside the tab. */
.vntv-trophy-tab:focus-visible {
    outline: 2px solid var(--vntv-focus);
    outline-offset: -2px;
}

/* The panel is in the Tab order, as the tabs pattern has it, so the keyboard
   gets from the row to a long board in one press. A ring round the whole
   body only for the keyboard; a click that lands on it draws nothing. */
.vntv-trophy-panel:focus {
    outline: none;
}

.vntv-trophy-panel:focus-visible {
    outline: 2px solid var(--vntv-focus);
    outline-offset: 6px;
    border-radius: 4px;
}

/* ---- The Trophies card's Clippers tab ----
   One holder and no board: the clapperboard, the face and the name on one
   line with the score under the name, then the best clip with its Watch, or,
   for somebody who has not linked a stream, the call to link. Laid out like a
   board's row, so the three tabs read as one card. */
.vntv-lb-clipper-holder {
    display: flex;
    flex-direction: column;
    gap: 10px;
    padding: 10px 12px;
    border: 1px solid var(--vntv-line);
    border-radius: 4px;
    background: linear-gradient(90deg, rgba(242, 201, 76, 0.08), rgba(242, 201, 76, 0) 70%);
}

.vntv-lb-clipper-holder[data-me="true"] {
    border-color: rgba(var(--vntv-accent-rgb), 0.32);
}

.vntv-lb-clipper-row {
    display: flex;
    align-items: center;
    gap: 10px;
    min-width: 0;
}

.vntv-lb-clipper-badge {
    display: inline-flex;
    flex: 0 0 auto;
    line-height: 0;
}

.vntv-face.vntv-lb-clipper-face {
    width: 32px;
    height: 32px;
    font-size: 12px;
}

.vntv-lb-clipper-who {
    display: flex;
    flex: 1 1 auto;
    flex-direction: column;
    gap: 2px;
    min-width: 0;
}

.vntv-lb-clipper-name {
    overflow: hidden;
    color: var(--vntv-fg);
    font-family: var(--vntv-display);
    font-size: 16px;
    font-weight: 600;
    text-overflow: ellipsis;
    white-space: nowrap;
}

.vntv-lb-clipper-score {
    color: var(--vntv-second);
    font-size: 12px;
    font-variant-numeric: tabular-nums;
}

/* "Best clip: “that jump”", a thumbs up and 31, and Watch. The title gives
   way first, and the count and the button never wrap apart from it. */
.vntv-lb-clipper-best {
    display: flex;
    align-items: center;
    gap: 7px;
    min-width: 0;
    margin: 0;
    color: var(--vntv-second);
    font-size: 12px;
}

.vntv-lb-clipper-best-label {
    flex: 0 0 auto;
    color: var(--vntv-second);
}

.vntv-lb-clipper-best-title {
    overflow: hidden;
    flex: 0 1 auto;
    min-width: 0;
    color: var(--vntv-fg);
    text-overflow: ellipsis;
    white-space: nowrap;
}

.vntv-lb-clipper-best-votes {
    display: inline-flex;
    flex: 0 0 auto;
    align-items: center;
    gap: 3px;
    color: var(--vntv-fg);
    font-family: var(--vntv-display);
    font-weight: 600;
    font-variant-numeric: tabular-nums;
}

.vntv-lb-clipper-best-votes svg {
    fill: currentColor;
}

.vntv-lb-clipper-watch {
    flex: 0 0 auto;
    margin-left: auto;
}

/* A phone: the title is what the line is for, so it keeps two lines of its
   own beside "Best clip:", and the count and Watch go under it. */
@media (max-width: 400px) {
    .vntv-lb-clipper-best {
        flex-wrap: wrap;
        align-items: baseline;
        row-gap: 6px;
    }

    .vntv-lb-clipper-best-title {
        display: -webkit-box;
        flex: 1 1 140px;
        -webkit-box-orient: vertical;
        -webkit-line-clamp: 2;
        overflow-wrap: anywhere;
        white-space: normal;
    }
}

/* The pill's own stock ring (Pills), nothing of its own. */
.vntv-lb-clipper-watch:focus-visible {
    outline: none;
}

/* The whole tab is the one section, so the empty line keeps to its side. */
.vntv-lb-clipper .vntv-lb-empty {
    text-align: left;
}

/* ---- Hashtag search ----
   The messages tagged with a hashtag from a stream title, on the trophy's
   card; see HashtagSearch.tsx. Narrower than the boards, being one column of
   messages, and the hashtag in its own case rather than the card title's
   capitals: it is read as it was written. */
.vntv-tag-card {
    max-width: 560px;
}

/* A stock modal, the skin's ground and words on this card as on
   the panel. The frame is the leaderboard's (.vntv-modal-card, the boards'
   own section); these say the same thing for this card alone, so that it
   reads as a stock dialog whatever that section does, and are the first
   thing to drop once the boards' card says it too. */
.vntv-modal-card.vntv-tag-card {
    border: 1px solid var(--vntv-line);
    border-radius: var(--radius-l, 12px);
    background: var(--vntv-bg);
    color: var(--vntv-fg);
    box-shadow: var(--elevation-6, 0 20px 32px 0 rgba(0, 0, 0, 0.12));
}

.vntv-tag-card .vntv-modal-head {
    border-bottom-color: var(--vntv-line);
}

.vntv-tag-card .vntv-modal-icon {
    color: var(--vntv-icon);
}

.vntv-tag-card .vntv-modal-title {
    color: var(--vntv-fg);
}

.vntv-tag-card .vntv-modal-close {
    width: 32px;
    height: 32px;
    border: none;
    border-radius: 4px;
    color: var(--vntv-icon);
}

.vntv-tag-card .vntv-modal-close:hover {
    background: var(--vntv-fill-hover);
    color: var(--vntv-icon-hover);
}

.vntv-tag-card .vntv-modal-close:focus-visible {
    outline: none;
    box-shadow: 0 0 0 2px var(--vntv-focus);
}

.vntv-tag-heading {
    display: flex;
    flex: 1 1 auto;
    flex-direction: column;
    min-width: 0;
}

.vntv-modal-title.vntv-tag-title {
    letter-spacing: 0.01em;
    overflow-wrap: anywhere;
    text-transform: none;
}

.vntv-tag-sub {
    margin: 1px 0 0;
    color: var(--vntv-second);
    font-size: 12px;
}

.vntv-tag-body {
    padding: 8px;
}

.vntv-tag-list {
    display: flex;
    flex-direction: column;
    gap: 2px;
    margin: 0;
    padding: 0;
    list-style: none;
}

/* A long word in a message must not widen the list past the card. */
.vntv-tag-item {
    min-width: 0;
}

/* The whole row is the message's permalink. Two classes and the element, to
   outrank Mattermost's .app__body a colours and underlines, which would
   otherwise paint every row in the theme's link colour. */
.vntv .vntv-tag-hit {
    display: flex;
    align-items: flex-start;
    gap: 10px;
    padding: 9px 10px;
    border: 1px solid transparent;
    border-radius: 4px;
    color: var(--vntv-fg);
    text-decoration: none;
}

.vntv a.vntv-tag-hit:hover,
.vntv a.vntv-tag-hit:focus {
    color: var(--vntv-fg);
    text-decoration: none;
    outline: none;
}

/* A stock list row: a quiet fill pointed at, the focus ring on the
   keyboard's. */
.vntv a.vntv-tag-hit:hover {
    background: var(--vntv-fill-hover);
}

.vntv a.vntv-tag-hit:focus-visible {
    box-shadow: inset 0 0 0 2px var(--vntv-focus);
}

.vntv-tag-face {
    flex: 0 0 auto;
    width: 32px;
    height: 32px;
    margin-top: 1px;
    font-size: 12px;
}

.vntv-tag-main {
    display: flex;
    flex: 1 1 auto;
    flex-direction: column;
    gap: 2px;
    min-width: 0;
}

.vntv-tag-meta {
    display: flex;
    align-items: baseline;
    gap: 8px;
    min-width: 0;
}

.vntv-tag-author {
    flex: 0 1 auto;
    min-width: 0;
    overflow: hidden;
    color: var(--vntv-fg);
    font-size: 13px;
    font-weight: 600;
    text-overflow: ellipsis;
    white-space: nowrap;
}

.vntv-tag-when {
    flex: 0 0 auto;
    margin-left: auto;
    color: rgba(var(--vntv-fg-rgb), 0.64);
    font-size: 11px;
    white-space: nowrap;
}

/* Allowed to wrap: on a narrow card the end of it is what says where a reply
   will open. */
.vntv-tag-where {
    color: rgba(var(--vntv-fg-rgb), 0.64);
    font-size: 11px;
    overflow-wrap: anywhere;
}

/* Three lines of the message at most, as text: markdown shows as it was
   typed, and the line breaks it was typed with are kept. */
.vntv-tag-text {
    display: -webkit-box;
    overflow: hidden;
    margin-top: 2px;
    color: var(--vntv-fg);
    font-size: 13px;
    line-height: 18px;
    overflow-wrap: anywhere;
    white-space: pre-line;
    -webkit-box-orient: vertical;
    -webkit-line-clamp: 3;
}

.vntv-tag-text.vntv-tag-empty {
    color: rgba(var(--vntv-fg-rgb), 0.64);
    font-style: italic;
}

/* Marked as Mattermost marks a search hit: the theme's own mention
   highlight, not the accent. */
.vntv-tag-mark {
    padding: 0 2px;
    border-radius: 3px;
    background: var(--mention-highlight-bg, #ffd470);
    color: var(--vntv-fg);
}

.vntv-tag-status {
    margin: 0;
    padding: 26px 12px;
    color: var(--vntv-second);
    font-size: 13px;
    text-align: center;
}

/* These lines take focus only to hold it inside the card when the button that
   had it is replaced (focusAfter in HashtagSearch.tsx); they are read, not
   pressed, so no ring. */
.vntv-tag-status:focus,
.vntv-tag-end:focus {
    outline: none;
}

.vntv-tag-error {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 12px;
    color: var(--vntv-fg);
}

.vntv-tag-error p {
    margin: 0;
    color: var(--vntv-danger);
}

.vntv-tag-more-row {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 6px;
    padding: 8px 0 4px;
}

/* Stock's second-rank button, 32px. */
.vntv-tag-more {
    height: 32px;
    padding: 0 16px;
    border: none;
    border-radius: 4px;
    background: rgba(var(--vntv-accent-rgb), 0.08);
    color: var(--vntv-accent);
    cursor: pointer;
    font: inherit;
    font-size: 12px;
    font-weight: 600;
}

.vntv-tag-more:hover:not([aria-disabled="true"]) {
    background: rgba(var(--vntv-accent-rgb), 0.12);
}

/* Busy rather than disabled, so it keeps keyboard focus while it loads. */
.vntv-tag-more[aria-disabled="true"] {
    cursor: default;
    background: var(--vntv-fill-hover);
    color: rgba(var(--vntv-fg-rgb), 0.32);
}

.vntv-tag-more:focus-visible,
.vntv-tag-open:focus-visible {
    outline: none;
    box-shadow: 0 0 0 2px var(--vntv-focus);
}

.vntv-tag-foot {
    display: flex;
    flex: 0 0 auto;
    flex-wrap: wrap;
    align-items: center;
    gap: 8px 12px;
    padding: 12px 16px;
    border-top: 1px solid var(--vntv-line);
}

.vntv-tag-note {
    margin: 0;
    color: var(--vntv-second);
    font-size: 12px;
}

/* Beside the button while there is room, and under it when there is not. */
.vntv-tag-foot .vntv-tag-note {
    flex: 1 1 200px;
}

/* Waiting on a popped-out panel's window: what button.vntv-pill:disabled
   looks like, on a button left enabled so it keeps keyboard focus. */
.vntv-tag-foot button.vntv-pill[aria-disabled="true"] {
    cursor: default;
    border-color: var(--vntv-line-focus);
    color: var(--vntv-second);
    background: var(--vntv-fill);
}

/* ---- Planning a stream ----
   One button showing when, and a card that opens under it: a month grid, then a
   clock. The clock is an SVG in its own 200x200 world, so every position on the
   face is arithmetic rather than a magic number, and it scales with the panel.
*/
.vntv-when-button {
    display: flex;
    align-items: center;
    gap: 7px;
    overflow: hidden;
    flex: 1 1 auto;
    min-width: 0;
    padding: 6px 9px;
    border: 1px solid var(--vntv-line);
    border-radius: 4px;
    background: var(--vntv-bg);
    color: var(--vntv-fg);
    cursor: pointer;
    font-family: var(--vntv-display);
    font-weight: 600;
    font-size: 13px;
    text-align: left;
    white-space: nowrap;
}

/* Stock's field steps: a darker line pointed at, the accent while
   its card is open. */
.vntv-when-button:hover {
    border-color: rgba(var(--vntv-fg-rgb), 0.48);
}

.vntv-when-button[aria-expanded="true"] {
    border-color: var(--vntv-accent);
    background: rgba(var(--vntv-accent-rgb), 0.08);
}

.vntv-when-icon {
    flex: 0 0 auto;
    font-size: 13px;
    line-height: 1;
}

/* The card. Raised rather than floating: the panel is a narrow scrolling
   column, and an absolutely positioned overlay in one of those is clipped by
   whichever ancestor happens to be scrolling that day. */
/* A stock raised surface: the skin's ground, a 1px line, radius
   4px and stock's elevation. */
.vntv-pop {
    display: flex;
    flex-direction: column;
    gap: 8px;
    padding: 10px;
    border: 1px solid var(--vntv-line);
    border-radius: 4px;
    background: var(--vntv-bg);
    color: var(--vntv-fg);
    box-shadow: var(--vntv-raised);
}

.vntv-pop-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
}

.vntv-pop-clock {
    display: flex;
    align-items: center;
    gap: 2px;
    color: var(--vntv-second);
    font-family: var(--vntv-display);
    font-weight: 600;
    font-size: 17px;
}

.vntv-pop-tab {
    padding: 2px 6px;
    border: 1px solid transparent;
    border-radius: 4px;
    background: transparent;
    color: var(--vntv-second);
    cursor: pointer;
    font-family: var(--vntv-display);
    font-weight: 600;
    font-size: 12px;
}

.vntv-pop-num {
    font-size: 17px;
    font-variant-numeric: tabular-nums;
}

.vntv-pop-tab:hover {
    background: var(--vntv-fill-hover);
    color: var(--vntv-fg);
}

/* Chosen, as stock's active toggle: the accent's quiet fill and the
   accent's words. */
.vntv-pop-tab[aria-pressed="true"] {
    background: rgba(var(--vntv-accent-rgb), 0.08);
    color: var(--vntv-accent);
}

.vntv-pop-tab:focus-visible {
    outline: none;
    box-shadow: 0 0 0 2px var(--vntv-focus);
}

.vntv-pop-foot {
    display: flex;
    justify-content: flex-end;
}

.vntv-cal {
    display: flex;
    flex-direction: column;
    gap: 6px;
}

.vntv-cal-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 6px;
}

.vntv-cal-month {
    color: var(--vntv-fg);
    font-family: var(--vntv-display);
    font-weight: 600;
    font-size: 13px;
}

/* Stock icon buttons, a little smaller for the month's head. */
.vntv-cal-step {
    width: 28px;
    height: 28px;
    border: none;
    border-radius: 4px;
    background: transparent;
    color: var(--vntv-icon);
    cursor: pointer;
    font: inherit;
    font-size: 15px;
    line-height: 1;
}

.vntv-cal-step:hover:enabled {
    background: var(--vntv-fill-hover);
    color: var(--vntv-icon-hover);
}

.vntv-cal-step:focus-visible {
    outline: none;
    box-shadow: 0 0 0 2px var(--vntv-focus);
}

.vntv-cal-step:disabled {
    cursor: default;
    color: rgba(var(--vntv-fg-rgb), 0.32);
}

.vntv-cal-grid {
    display: grid;
    grid-template-columns: repeat(7, 1fr);
    gap: 2px;
}

/* Stock's date picker sets its days in the page's own face: 12px
   weekday letters, 13px days. */
.vntv-cal-letter {
    padding: 2px 0;
    color: rgba(var(--vntv-fg-rgb), 0.64);
    font-size: 12px;
    text-align: center;
}

/* Square without a fixed pixel size, so the grid follows the panel's width
   instead of fighting it. */
.vntv-cal-day {
    aspect-ratio: 1;
    padding: 0;
    border: 1px solid transparent;
    border-radius: 50%;
    background: transparent;
    color: var(--vntv-fg);
    cursor: pointer;
    font-family: inherit;
    font-weight: 400;
    font-size: 13px;
    font-variant-numeric: tabular-nums;
}

/* Stock's date picker: a quiet fill pointed at, the chosen day
   filled with the accent. */
.vntv-cal-day:hover:enabled {
    background: var(--vntv-fill-hover);
}

.vntv-cal-day[aria-pressed="true"] {
    border-color: var(--vntv-accent);
    background: var(--vntv-accent);
    color: var(--vntv-on-accent);
}

.vntv-cal-day:focus-visible {
    outline: none;
    box-shadow: 0 0 0 2px var(--vntv-focus);
}

.vntv-cal-day:disabled {
    cursor: default;
    color: rgba(var(--vntv-fg-rgb), 0.32);
}

.vntv-dial {
    display: block;
    width: 100%;
    max-width: 230px;
    margin: 0 auto;
    border-radius: 50%;
    touch-action: none;
    user-select: none;
}

/* A slider from the keyboard, so it shows where the keys are going. */
.vntv-dial:focus {
    outline: none;
}

.vntv-dial:focus-visible {
    outline: 2px solid var(--vntv-focus);
    outline-offset: 2px;
}

.vntv-dial-face {
    fill: var(--vntv-fill-hover);
}

.vntv-dial-hand {
    stroke: var(--vntv-accent);
    stroke-width: 2;
}

.vntv-dial-pin {
    fill: var(--vntv-accent);
}

.vntv-dial-knob {
    fill: var(--vntv-accent);
}

.vntv-dial-mark {
    fill: var(--vntv-fg);
    font-family: var(--vntv-display);
    font-size: 15px;
    font-weight: 600;
    text-anchor: middle;
    dominant-baseline: central;
    pointer-events: none;
}

.vntv-dial-inner {
    font-size: 12px;
    fill: var(--vntv-second);
}

.vntv-dial-mark[data-open="false"] {
    fill: rgba(var(--vntv-fg-rgb), 0.32);
}

/* The chosen hour or minute sits on the accent's knob: the theme's
   words for the accent, as every accent fill has. */
.vntv-dial-mark[data-chosen="true"] {
    fill: var(--vntv-on-accent);
}

.vntv-sched-form {
    display: flex;
    flex-direction: column;
    gap: 7px;
}

/* Which plan the form is changing, when it is changing one. */
.vntv-sched-editing {
    margin: 0;
    overflow: hidden;
    color: var(--vntv-second);
    font-family: var(--vntv-display);
    font-weight: 600;
    font-size: 10px;
    letter-spacing: 0.14em;
    text-overflow: ellipsis;
    text-transform: uppercase;
    white-space: nowrap;
}

.vntv-sched-editing b {
    color: var(--vntv-fg);
}

/* The plan sheet once a new plan is saved (design/3.2.0.md 6.6): what was
   saved, PLANNED. and Share in the open channel, for 20 s. */
.vntv-sched-saved {
    display: flex;
    flex-direction: column;
    gap: 10px;
}

/* It takes the focus only when Share is not there to take it, and shows no
   ring for that. */
.vntv-sched-saved:focus {
    outline: none;
}

.vntv-sched-saved-what {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 2px 8px;
    min-width: 0;
    margin: 0;
    color: var(--vntv-second);
    font-size: 12px;
    line-height: 18px;
}

.vntv-sched-saved-what b {
    min-width: 0;
    max-width: 100%;
    overflow: hidden;
    color: var(--vntv-fg);
    font-size: 14px;
    font-weight: 600;
    text-overflow: ellipsis;
    white-space: nowrap;
}

.vntv-sched-saved-kind {
    display: inline-flex;
    color: var(--vntv-icon);
}

.vntv-sched-saved-row {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: 8px 12px;
}

.vntv-sched-saved-done {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    margin: 0;
    color: var(--vntv-fg);
    font-family: var(--vntv-display);
    font-size: 14px;
    font-weight: 600;
    letter-spacing: 0.06em;
    text-transform: uppercase;
}

.vntv-sched-saved-done svg {
    color: var(--vntv-accent);
}

/* Read out, never seen: the count of suggestions as they arrive. */
.vntv-sr-only {
    position: absolute;
    width: 1px;
    height: 1px;
    overflow: hidden;
    clip-path: inset(50%);
    white-space: nowrap;
}

.vntv-titlesearch {
    display: flex;
    flex-direction: column;
    gap: 6px;
}

/* ---- Suggestions while typing a title ----
   A stock menu in the theme's colours, the display face for titles, and the
   accent for the one the keyboard or the mouse is on. */
.vntv-suggest {
    overflow: hidden;

    /* A stock menu: the panel's ground, a 1px line, radius 4px. */
    border: 1px solid var(--vntv-line);
    border-radius: 4px;
    background: var(--vntv-bg);
    box-shadow: var(--elevation-4, 0 8px 24px 0 rgba(0, 0, 0, 0.12));
    animation: vntv-arrive 140ms ease-out both;
}

.vntv-suggest-head {
    display: flex;
    align-items: center;
    gap: 8px;
    margin: 0;
    padding: 7px 10px 5px;
    color: var(--vntv-second);
    font-family: var(--vntv-display);
    font-weight: 600;
    font-size: 10px;
    letter-spacing: 0.14em;
    text-transform: uppercase;
}

.vntv-suggest-spinner {
    width: 10px;
    height: 10px;
    border: 2px solid var(--vntv-line);
    border-top-color: var(--vntv-accent);
    border-radius: 50%;
    animation: vntv-spin 700ms linear infinite;
}

@keyframes vntv-spin {
    to { transform: rotate(360deg); }
}

.vntv-suggest-list {
    max-height: 290px;
    overflow-y: auto;
    overscroll-behavior: contain;
    margin: 0;
    padding: 0 0 4px;
    list-style: none;
}

.vntv-suggest-option {
    display: flex;
    align-items: center;
    gap: 10px;
    margin: 0 4px;
    padding: 6px 8px;
    border-left: 2px solid transparent;
    border-radius: 5px;
    cursor: pointer;
}

.vntv-suggest-option[data-active="true"] {
    border-left-color: var(--vntv-accent);
    background: rgba(var(--vntv-accent-rgb), 0.08);
}

/* Thumbs in the shape the art actually is: a portrait poster for a movie or
   show, Steam's wide capsule for a game. */
.vntv-suggest-thumb,
.vntv-picked-thumb,
.vntv-suggest-wait-thumb {
    display: block;
    flex: 0 0 auto;
    overflow: hidden;
    width: 34px;
    height: 51px;
    border-radius: 4px;
    background: var(--vntv-fill-hover);
}

.vntv-suggest-thumb[data-kind="game"],
.vntv-picked-thumb[data-kind="game"],
.vntv-suggest-wait[data-kind="game"] .vntv-suggest-wait-thumb {
    width: 76px;
    height: 29px;
}

/* IGDB's game art is box art, portrait like a poster, rather than Steam's
   wide capsule. */
.vntv-suggest-thumb[data-kind="game"][data-art="tall"],
.vntv-picked-thumb[data-kind="game"][data-art="tall"] {
    width: 34px;
    height: 51px;
}

/* In the list, box art sits centred in the room a Steam capsule takes, so
   every game's title starts in the same column, mixed or not, and a result
   does not shift sideways as it replaces the wide waiting rows. 21px is
   (76 - 34) / 2. The picked row has one thumb and needs nothing. */
.vntv-suggest-thumb[data-kind="game"][data-art="tall"] {
    margin: 0 21px;
}

.vntv-suggest-thumb img,
.vntv-picked-thumb img {
    display: block;
    width: 100%;
    height: 100%;
    object-fit: cover;
}

.vntv-suggest-text,
.vntv-picked-text {
    display: flex;
    flex: 1 1 auto;
    flex-direction: column;
    min-width: 0;
}

.vntv-suggest-title,
.vntv-picked-title {
    overflow: hidden;
    color: var(--vntv-fg);
    font-family: var(--vntv-display);
    font-weight: 600;
    font-size: 14px;
    line-height: 18px;
    text-overflow: ellipsis;
    white-space: nowrap;
}

.vntv-suggest-meta,
.vntv-picked-meta {
    color: var(--vntv-second);
    font-size: 11px;
    line-height: 15px;
}

/* What was typed, picked out in each title: the reason it is on the list. */
.vntv-suggest-match {
    padding: 0;
    background: transparent;
    color: inherit;
    text-decoration: underline;
    text-decoration-color: var(--vntv-accent);
    text-decoration-thickness: 2px;
    text-underline-offset: 3px;
}

/* Keep what I typed: always last, quieter than a title. */
.vntv-suggest-keep {
    margin-top: 2px;
    color: var(--vntv-second);
    font-size: 12px;
    font-style: italic;
}

.vntv-suggest-note {
    padding: 8px 12px;
    color: var(--vntv-second);
    font-size: 12px;
}

/* Waiting: three rows in the shape of results, shimmering. */
.vntv-suggest-wait {
    display: flex;
    align-items: center;
    gap: 10px;
    margin: 0 4px;
    padding: 6px 8px;
}

.vntv-suggest-wait-lines {
    display: flex;
    flex: 1 1 auto;
    flex-direction: column;
    gap: 6px;
}

.vntv-suggest-wait-lines span {
    display: block;
    height: 8px;
    border-radius: 999px;
    background: var(--vntv-fill-hover);
}

.vntv-suggest-wait-lines span:first-child {
    width: 62%;
    height: 10px;
}

.vntv-suggest-wait-lines span:last-child {
    width: 34%;
}

.vntv-suggest-wait-thumb,
.vntv-suggest-wait-lines span {
    animation: vntv-shimmer 1.1s ease-in-out infinite;
}

@keyframes vntv-shimmer {
    0%, 100% { opacity: 0.55; }
    50% { opacity: 1; }
}

.vntv-suggest-foot {
    margin: 0;
    padding: 5px 10px 7px;
    border-top: 1px solid var(--vntv-line);
    color: rgba(var(--vntv-fg-rgb), 0.56);
    font-size: 10px;
    letter-spacing: 0.02em;
}

/* Linked: the picked title under the box, with a way to undo it. */
.vntv-picked {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 6px 8px;
    border: 1px solid var(--vntv-line);
    border-left: 3px solid var(--vntv-accent);
    border-radius: 4px;
    background: transparent;
    animation: vntv-arrive 160ms ease-out both;
}

/* A status, not a button: a quiet tag of the accent, no outline. */
.vntv-picked-tag {
    flex: 0 0 auto;
    padding: 2px 6px;
    border-radius: 4px;
    background: rgba(var(--vntv-accent-rgb), 0.08);
    color: var(--vntv-accent);
    font-weight: 600;
    font-size: 11px;
    line-height: 16px;
}

.vntv-picked-undo {
    display: flex;
    flex: 0 0 auto;
    align-items: center;
    justify-content: center;
    width: 24px;
    height: 24px;
    padding: 0;
    border: none;
    border-radius: 4px;
    background: transparent;
    color: var(--vntv-icon);
    cursor: pointer;
}

.vntv-picked-undo:hover {
    background: var(--vntv-fill-hover);
    color: var(--vntv-icon-hover);
}

.vntv-picked-undo:focus-visible {
    outline: none;
    box-shadow: 0 0 0 2px var(--vntv-focus);
}

/* ---- Joined fields ----
   The planning form is two of these: the movie and game toggles joined to the
   left of the text box, and the save button joined to the right of the time.
   One border round each, so the pieces read as one control rather than a row
   of neighbours — and each costs no line of its own. */
.vntv-field {
    display: flex;
    align-items: stretch;
    min-height: 32px;
    overflow: hidden;

    /* Stock's text field, with the panel's colours. */
    border: 1px solid var(--vntv-line);
    border-radius: 4px;
    background: var(--vntv-bg);
    transition: border-color 120ms ease;
}

/* Stock's text field steps: a darker line pointed at; with the
   caret inside, the accent's line doubled by an inset 1px of it. */
.vntv-field:hover:not(:focus-within) {
    border-color: rgba(var(--vntv-fg-rgb), 0.48);
}

.vntv-field:focus-within {
    border-color: var(--vntv-accent);
    box-shadow: inset 0 0 0 1px var(--vntv-accent);
}

/* Movie or game, or neither: a pressed one says which, and pressing it again
   says neither. Icons rather than words — the words are their labels and
   tooltips — so both fit in front of the text without crowding it. */
.vntv-field-kinds {
    display: flex;
    flex: 0 0 auto;
    border-right: 1px solid var(--vntv-line);
}

.vntv-field-kind {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 34px;
    padding: 0;
    border: none;
    background: transparent;
    color: var(--vntv-icon);
    cursor: pointer;
    transition: background-color 120ms ease, color 120ms ease;
}

.vntv-field-kind + .vntv-field-kind {
    border-left: 1px solid var(--vntv-line);
}

.vntv-field-kind:hover {
    background: var(--vntv-fill-hover);
    color: var(--vntv-icon-hover);
}

.vntv-field-kind[aria-pressed="true"] {
    background: rgba(var(--vntv-accent-rgb), 0.08);
    color: var(--vntv-accent);
}

.vntv-field-kind:focus-visible {
    outline: 2px solid var(--vntv-focus);
    outline-offset: -2px;
}

/* The pieces inside give up their own borders to the field's. */
.vntv-field .vntv-field-input {
    border: none;
    border-radius: 0;
    background: transparent;
    padding: 6px 9px;
    font-size: 13px;
}

/* The field's own edge says it is being typed in; the box inside it has no
   ring of its own. */
.vntv-field .vntv-field-input:focus {
    box-shadow: none;
}

.vntv-field .vntv-field-when {
    border: none;
    border-radius: 0;
    background: transparent;
}

.vntv-field .vntv-field-when:hover {
    background: var(--vntv-fill-hover);
}

.vntv-field .vntv-field-when[aria-expanded="true"] {
    background: rgba(var(--vntv-accent-rgb), 0.08);
}

.vntv-field-when:focus-visible {
    outline: 2px solid var(--vntv-focus);
    outline-offset: -2px;
}

/* Save: the red end of the time row, a tick on it. Greyed until there is
   something to save — the text box is the only thing that can be missing. */
.vntv-field-save {
    display: flex;
    flex: 0 0 auto;
    align-items: center;
    justify-content: center;
    width: 42px;
    padding: 0;
    border: none;
    border-left: 1px solid var(--vntv-accent);
    background: var(--vntv-accent);
    color: var(--vntv-on-accent);
    cursor: pointer;
    transition: background-color 120ms ease, color 120ms ease;
}

/* Stock's primary button, pointed at: a little darker. */
.vntv-field-save:hover {
    background: linear-gradient(rgba(0, 0, 0, 0.08), rgba(0, 0, 0, 0.08)), var(--vntv-accent);
}

.vntv-field-save:focus-visible {
    outline: 2px solid var(--vntv-focus);
    outline-offset: -3px;
}

.vntv-field-save:disabled {
    border-left-color: var(--vntv-line);
    background: transparent;
    color: rgba(var(--vntv-fg-rgb), 0.32);
    cursor: default;
}

.vntv-field-save[data-busy="true"] {
    border-left-color: var(--vntv-accent);
    background: var(--vntv-accent);
    color: var(--vntv-on-accent);
    animation: vntv-saving 900ms ease-in-out infinite;
}

@keyframes vntv-saving {
    0%, 100% { opacity: 1; }
    50% { opacity: 0.55; }
}

/* A row that opens and closes rather than appearing and vanishing.

   0fr to 1fr is the only way to transition to a height nobody has measured;
   max-height would need a number picked in advance, and a rail of cards has no
   such number. The inner element needs min-height: 0 or the grid refuses to
   take it below its content height. */
.vntv-reveal {
    display: grid;
    grid-template-rows: 1fr;
}

.vntv-reveal[data-open="false"] {
    grid-template-rows: 0fr;
}

.vntv-reveal-inner {
    min-height: 0;
    overflow: hidden;
}

/* Armed only after the first paint, so opening the panel does not play an
   animation for every row that happens to have something in it. */
.vntv-reveal[data-armed="true"] {
    transition: grid-template-rows 280ms cubic-bezier(0.22, 1, 0.36, 1);
}

.vntv-reveal[data-armed="true"] .vntv-reveal-inner {
    transition: opacity 200ms ease, transform 280ms cubic-bezier(0.22, 1, 0.36, 1);
}

.vntv-reveal[data-armed="true"][data-open="false"] .vntv-reveal-inner {
    opacity: 0;
    transform: translateY(-6px);
}

/* ---- Messages ---- */
/* A stock surface: 1px line, radius 4px, the panel's words. An
   error is the theme's error words on a faint wash of them. */
.vntv-notice {
    border: 1px solid var(--vntv-line);
    border-radius: 4px;
    padding: 10px;
    font-size: 12px;
    color: var(--vntv-fg);
}

/* An error: the theme's error words. The class also marks words alone (a
   hint under a field, a line in the System Console), so the faint wash and
   its line go only on a notice's box, never on a bare line of words. In the
   System Console, .vntv-admin .vntv-notice-error is the stronger rule. */
.vntv-notice-error {
    color: var(--vntv-danger);
}

.vntv-notice.vntv-notice-error {
    border-color: rgba(var(--vntv-danger-rgb), 0.32);
    background: rgba(var(--vntv-danger-rgb), 0.08);
}

.vntv-notice code {
    color: var(--vntv-second);
    background: var(--vntv-fill);
    padding: 1px 4px;
    border-radius: 3px;
}

/* ---- Admin alerts: the release ----
   For system admins, among the panel's admin alerts (AdminAlerts.tsx).
   The release: the server's sentence, then the files it names. */
.vntv-release-alert {
    line-height: 1.45;
    overflow-wrap: anywhere;
}

.vntv-release-alert .vntv-release-files {
    margin: 6px 0;
    padding-left: 16px;
    list-style: disc;
}

.vntv-release-files code {
    word-break: break-all;
}

/* ---- Footer / link form ----
   No rule above it: the thin red line that used to divide it from the rest
   of the panel was asked away. */
.vntv-footer {
    flex: 0 0 auto;

    /* Placed as the reply box is in a thread: 12px over the bar,
       24px in from the panel's edges, and under it a strip exactly 24px
       tall, where stock has its Help line. With a bottom banner the banner
       IS that strip, so the bar stays level with the message box either
       way. */
    padding: 12px 24px 24px;
    display: flex;
    flex-direction: column;
    gap: 8px;
}

.vntv-footer[data-strip="banner"] {
    padding-bottom: 0;
}

/* Icon-only pills: square rather than lozenge-shaped, so a row of them reads as
   controls instead of labels. */
.vntv-pill-icon {
    padding: 3px 6px;
}

.vntv-pill-icon svg {
    display: block;
}

button.vntv-pill.vntv-pill-on {
    background: rgba(var(--vntv-accent-rgb), 0.16);
    color: var(--vntv-fg);
}

.vntv-key {
    font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
    letter-spacing: 0.02em;
    word-break: break-all;
}

.vntv-input {
    flex: 1 1 auto;
    min-width: 0;
    background: var(--vntv-bg);
    border: 1px solid var(--vntv-line);
    border-radius: 4px;
    color: var(--vntv-fg);
    font: inherit;
    font-size: 12px;
    padding: 5px 8px;
}

.vntv-input::placeholder {
    color: var(--vntv-placeholder);
    opacity: 1;
}

/* Stock's text field: the accent round it while it is typed in. */
.vntv-input:focus {
    outline: none;
    border-color: var(--vntv-accent);
    box-shadow: inset 0 0 0 1px var(--vntv-accent);
}

.vntv-hint {
    font-size: 11px;
    color: var(--vntv-second);
}

/* An error said in a hint's size is still an error. Without this, .vntv-hint
   comes later than .vntv-notice-error at the same specificity and its grey
   won, so a failed save read like a neutral note. In the lighter red, as every
   word in red on the panel's black is (about 3.5:1 in the panel's own red). */
.vntv-hint.vntv-notice-error {
    color: var(--vntv-danger);
}

/* The plugin's red call to action: UNLOCK on the lock screen, and Link your
   stream in the clip editor. */
/* A stock primary button. */
.vntv-link-cta {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    height: 32px;
    padding: 0 16px;
    border: none;
    border-radius: 4px;
    background: var(--vntv-accent);
    color: var(--vntv-on-accent);
    cursor: pointer;
    font-family: inherit;
    font-size: 12px;
    font-weight: 600;
    transition: background-color 150ms ease;
}

.vntv-link-cta svg {
    display: block;
}

.vntv-link-cta:focus-visible {
    outline: none;
    box-shadow: 0 0 0 2px var(--vntv-focus);
}

/* The small text key: words in Oswald capitals with no box until it is
   pointed at. Try again on the lock and in the panel's notice, and Link your
   stream on the watch-only line. Shared, not the lock's own; words never go
   in an icon button. */
.vntv-textkey {
    display: inline-flex;
    flex: 0 0 auto;
    align-items: center;
    gap: 6px;
    width: auto;
    height: 26px;
    margin: 0;
    padding: 0 8px;
    border: 1px solid transparent;
    border-radius: 4px;
    background: transparent;
    color: var(--vntv-second);
    cursor: pointer;
    font-family: var(--vntv-display);
    font-size: 11px;
    font-weight: 600;
    letter-spacing: 0.12em;
    text-transform: uppercase;
    white-space: nowrap;
    transition: border-color 120ms ease, color 120ms ease, background-color 120ms ease;
}

.vntv-textkey svg {
    display: block;
    flex: 0 0 auto;
}

.vntv-textkey:hover,
.vntv-textkey[aria-expanded="true"] {
    border-color: var(--vntv-line-focus);
    color: var(--vntv-fg);
}

.vntv-textkey:focus-visible {
    outline: none;
    box-shadow: 0 0 0 2px var(--vntv-focus);
}

/* Asking, and not to be pressed again until it has an answer: still
   focusable, so it keeps the keyboard. */
.vntv-textkey[aria-disabled="true"] {
    cursor: progress;
}

/* A notice that is only news, not a fault: the panel could not check the
   reader's access just now, and keeps everything meanwhile. Try again's
   answer, when it is still none, goes on a line of its own under it. */
.vntv-notice-quiet {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
    border-color: var(--vntv-line);
    color: var(--vntv-second);
}

/* What Try again found, when it is still nothing (TryAgain.tsx). Under the
   key on the lock; a line of its own in the panel's notice. Empty, it takes
   no room and is still in the page, so what is put in it is said. */
.vntv-tryagain-said {
    margin: 4px 0 0;
    color: var(--vntv-second);
    font-size: 12px;
    line-height: 1.45;
    text-align: center;
}

.vntv-tryagain-said:empty {
    position: absolute;
    width: 1px;
    height: 1px;
    margin: 0;
    overflow: hidden;
    clip-path: inset(50%);
}

.vntv-notice-quiet .vntv-tryagain-said {
    flex: 1 0 100%;
    margin: 0;
    text-align: left;
}

/* ---- The bar ----
   The footer is one quiet bar: the stream title, and a few small tools at its
   end. Glass rather than a box, so the artwork behind the footer still shows
   through, softened; one weight of grey for everything in it, with red kept
   for what is live and for focus. Not linked, the title's place is Link Your
   Account. */
.vntv-dock {
    position: relative;
    display: flex;
    flex-direction: column;
    min-width: 0;
}

/* The bar is the message box's twin: 98px, the stock border, its hover and
   focus steps, radius and ground; a 46px row for the title over a 48px row
   of tools with Save at the right, where the message box has its
   formatting row and Send. */
.vntv-bar {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto;
    grid-template-rows: 46px 48px;
    grid-template-areas: "field field" "tools send";
    align-items: center;
    box-sizing: border-box;
    min-width: 0;
    height: 98px;
    margin: 0;
    padding: 0;
    border: 2px solid var(--vntv-line);
    border-radius: 4px;
    background: var(--vntv-bg);
    color: var(--vntv-fg);
    container: vntv-bar / inline-size;
}

.vntv-bar:hover {
    border-color: var(--vntv-line-hover);
}

.vntv-bar:focus-within,
.vntv-bar:focus-within:hover {
    border-color: var(--vntv-line-focus);
}

/* Where Escape from the title leaves focus: the box's own darker edge
   shows it, as the message box's does. */
.vntv-bar:focus {
    outline: none;
}

/* The stream title, a field like the message box's own: its
   padding, its 14px words on 20px lines, its placeholder. No ring of its
   own: the box's edge darkens round it, as the message box's does. */
.vntv-bar-input {
    grid-area: field;
    align-self: stretch;
    box-sizing: border-box;
    width: 100%;
    min-width: 0;
    height: 46px;
    margin: 0;
    padding: 13px 16px 12px;
    border: none;
    border-radius: 2px 2px 0 0;
    background: transparent;
    color: var(--vntv-fg);
    font: inherit;
    font-size: 14px;
    line-height: 20px;
    text-overflow: ellipsis;
    outline: none;
    box-shadow: none;
}

.vntv-bar-input::placeholder {
    color: var(--vntv-placeholder);
    opacity: 1;
}

/* While the profile is loading: the same box with nothing in the field. */
.vntv-bar-loading {
    grid-area: field;
    height: 46px;
}

/* Linked, but the title cannot be set: why, with the link key beside it lit
   (.vntv-bar-relinktool), since relinking is what fixes it. */
.vntv-bar-problem {
    grid-area: field;
    display: flex;
    align-items: center;
    gap: 8px;
    min-width: 0;
    height: 46px;
    padding: 0 16px;
}

.vntv-bar-problem-text {
    display: -webkit-box;
    flex: 1 1 auto;
    min-width: 0;
    overflow: hidden;
    color: var(--vntv-danger);
    font-size: 13px;
    line-height: 18px;
    -webkit-box-orient: vertical;
    -webkit-line-clamp: 2;
}

/* The tools at the end of the bar: small, square, grey until pointed at,
   white while what they open is open. Rigid as a group: only the stream
   title gives way. A group allowed to shrink was squeezed with the title, in
   proportion to its width, while every tool in it kept its own; at 320 px
   with What's on? set and the link key, the last tool then hung over the
   bar's rounded end and out of the panel. */
/* The tools are the formatting row's own buttons: 32px squares,
   radius 4px, the icon grey; a quiet ground when pointed at, and while what
   one opens is open the accent's faint ground and the icon in the accent,
   as stock's active formatting buttons are. The row starts 7px in, as the
   formatting row does. */
.vntv-bar-tools {
    grid-area: tools;
    display: flex;
    align-items: center;
    gap: 2px;
    min-width: 0;
    height: 48px;
    padding: 0 0 0 7px;
}

.vntv-bar-tool {
    position: relative;
    display: inline-flex;
    flex: 0 0 auto;
    align-items: center;
    justify-content: center;
    gap: 6px;
    box-sizing: border-box;
    min-width: 32px;
    height: 32px;
    margin: 0;
    padding: 0;
    border: none;
    border-radius: 4px;
    background: transparent;
    color: var(--vntv-icon);
    cursor: pointer;
    font: inherit;
    transition: background-color 150ms ease, color 150ms ease;
}

.vntv-bar-tool svg {
    display: block;
    flex: 0 0 auto;
}

.vntv-bar-tool[aria-expanded="true"] {
    background: rgba(var(--vntv-accent-rgb), 0.08);
    color: var(--vntv-accent);
}

.vntv-bar-tool:focus-visible {
    outline: none;
    box-shadow: 0 0 0 2px var(--vntv-focus);
}

/* Save, where the message box has Send: 48 by 32, the accent; quiet, as
   Send is with nothing to send, until the field says something new. In
   the twin it sits 7px off the box's bottom and right, as Send does. In
   one row it comes after the tools only while there is something to save. */
.vntv-bar-save {
    grid-area: send;
    align-self: end;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    box-sizing: border-box;
    min-width: 48px;
    height: 32px;
    margin: 0 7px 7px 0;
    padding: 0 8px;
    border: none;
    border-radius: 4px;
    background: var(--vntv-accent);
    color: var(--vntv-on-accent);
    cursor: pointer;
    font: inherit;
    font-size: 12px;
    font-weight: 600;
    line-height: 16px;
    white-space: nowrap;
    transition: background-color 150ms ease, color 150ms ease;
}

.vntv-bar-save svg,
.vntv-bar-save-words {
    display: block;
}

.vntv-bar-save[aria-disabled="true"] {
    background: var(--vntv-fill-hover);
    color: rgba(var(--vntv-fg-rgb), 0.32);
    cursor: default;
}

.vntv-bar-save[data-saving="true"] {
    padding: 0 12px;
    cursor: progress;
}

.vntv-bar-save:focus-visible {
    outline: none;
    box-shadow: 0 0 0 2px var(--vntv-focus);
}

/* What's on?, once something is said: a chip with its picture and its title
   rather than the television. It keeps the width its title needs, up to a
   cap, and the stream title gives way instead. */
.vntv-whatson[data-set="true"] {
    flex: 0 1 auto;
    justify-content: flex-start;
    max-width: 132px;
    padding: 0 8px 0 6px;
    color: var(--vntv-fg);
}

.vntv-whatson-title {
    min-width: 0;
    overflow: hidden;
    font-size: 12px;
    text-overflow: ellipsis;
    white-space: nowrap;
}

/* The scissors' dot: whether clips of your stream are on. A ring of the bar's
   own colour round it, so it sits on the icon rather than in it. */
/* Grey while clips are off, the accent while they are on, and
   the theme's LIVE red while the stream is being kept, ringed in the bar's
   ground. */
.vntv-bar-dot {
    position: absolute;
    top: 5px;
    right: 5px;
    box-sizing: border-box;
    width: 9px;
    height: 9px;
    border: 2px solid var(--vntv-bg);
    border-radius: 50%;
    background: rgba(var(--vntv-fg-rgb), 0.4);
}

.vntv-bar-clips[data-state="on"] .vntv-bar-dot {
    background: var(--vntv-accent);
}

.vntv-bar-clips[data-state="live"] .vntv-bar-dot {
    background: var(--vntv-live);
}

/* Kept this minute: the dot breathes, as a recording light does.
   A small breath (3px, faint) from the dot's own edge, so it never
   reads as a smudge behind the scissors; none under less motion. */
.vntv-bar-clips[data-state="live"] .vntv-bar-dot::after {
    content: '';
    position: absolute;
    inset: 0;
    border-radius: 50%;
    box-shadow: 0 0 0 0 rgba(var(--vntv-live-rgb), 0.35);
    animation: vntv-bar-breathe 2.4s ease-out infinite;
}

@keyframes vntv-bar-breathe {
    0% { box-shadow: 0 0 0 0 rgba(var(--vntv-live-rgb), 0.35); }
    70%, 100% { box-shadow: 0 0 0 3px rgba(var(--vntv-live-rgb), 0); }
}

/* A narrow bar: What's on? keeps its picture and gives its words up to the
   stream title, which is the one thing the bar is for. They are still its
   name, and in its tooltip. */
@container vntv-bar (max-width: 359px) {
    .vntv-whatson[data-set="true"] {
        padding: 0;
    }

    .vntv-whatson[data-set="true"] .vntv-whatson-title {
        position: absolute;
        width: 1px;
        height: 1px;
        clip-path: inset(50%);
    }
}

.vntv-whatson-kind {
    display: flex;
    flex: 0 0 auto;
    color: var(--vntv-accent);
}

.vntv-whatson-kind svg {
    display: block;
}

/* The title's own art, portrait for both kinds: a poster, or a game's box
   art. See ChipArt for why not Steam's wide capsule. */
.vntv-whatson-thumb {
    flex: 0 0 auto;
    width: 18px;
    height: 27px;
    overflow: hidden;
    border-radius: 2px;
    background: var(--vntv-fill-hover);
    box-shadow: 0 0 0 1px var(--vntv-line);
}

.vntv-whatson-thumb img {
    display: block;
    width: 100%;
    height: 100%;
    object-fit: cover;
}

/* Broadcast Box has the stream hidden from its list, or the title could not
   be saved: a warning over the bar. A stock surface in the theme's
   error words, on a faint wash of them over the panel's ground. */
.vntv-bar-alert {
    display: flex;
    align-items: center;
    gap: 10px;
    min-width: 0;
    padding: 8px 8px 8px 12px;
    border: 1px solid rgba(var(--vntv-danger-rgb), 0.32);
    border-radius: 4px;
    background: linear-gradient(rgba(var(--vntv-danger-rgb), 0.08), rgba(var(--vntv-danger-rgb), 0.08)), var(--vntv-bg);
    box-shadow: var(--vntv-raised);
}

.vntv-bar-alert-text {
    flex: 1 1 auto;
    min-width: 0;
    color: var(--vntv-danger);
    font-size: 12px;
    line-height: 1.4;
}

/* Its Show me, as every action inside a danger alert: neutral. */
.vntv-bar-alert .vntv-sheet-action {
    background: var(--vntv-fill-hover);
    color: var(--vntv-fg);
}

/* ---- The sheets ----
   What the bar's tools open: one at a time, drawn above the bar but after it
   in the page, so the keyboard goes from a tool into what it opened. Above,
   because the footer sits on the bottom of the panel: opening one grows the
   footer upward and the panel gives way, while the bar stays where it is,
   under the pointer that pressed it. */
.vntv-dock > .vntv-reveal {
    order: -1;
}

/* And it rises out of the bar rather than dropping onto it. */
.vntv-dock > .vntv-reveal[data-armed="true"][data-open="false"] .vntv-reveal-inner {
    transform: translateY(8px);
}

/* An open sheet's raised shadow is let out past the reveal's
   clip, which is only there for the opening and closing. */
.vntv-dock > .vntv-reveal[data-open="true"] > .vntv-reveal-inner {
    overflow: clip;
    overflow-clip-margin: 24px;
}

/* A stock surface, as Mattermost's menus and popovers are: the
   panel's ground, a 1px line, radius 4px and the raised shadow; no glass. */
.vntv-sheet {
    display: flex;
    flex-direction: column;
    gap: 10px;
    box-sizing: border-box;
    min-width: 0;
    margin: 0 0 8px;
    padding: 12px 16px 16px;
    border: 1px solid var(--vntv-line);
    border-radius: 4px;
    background: var(--vntv-bg);
    color: var(--vntv-fg);
    box-shadow: var(--vntv-raised);
}

.vntv-sheet-head {
    display: flex;
    align-items: flex-start;
    gap: 8px;
    min-width: 0;
}

.vntv-sheet-heading {
    display: flex;
    flex: 1 1 auto;
    flex-direction: column;
    gap: 3px;
    min-width: 0;
    padding-top: 2px;
}

.vntv-sheet-title {
    color: var(--vntv-fg);
    font-family: var(--vntv-display);
    font-size: 11px;
    font-weight: 600;
    letter-spacing: 0.14em;
    line-height: 14px;
    text-transform: uppercase;
}

.vntv-sheet-note {
    overflow: hidden;
    color: var(--vntv-second);
    font-size: 11px;
    line-height: 14px;
    text-overflow: ellipsis;
    white-space: nowrap;
}

/* Stock's 32px icon button; the margins keep the glyph where it was. */
.vntv-sheet-close {
    display: inline-flex;
    flex: 0 0 auto;
    align-items: center;
    justify-content: center;
    width: 32px;
    height: 32px;
    margin: -6px -8px 0 0;
    padding: 0;
    border: none;
    border-radius: 4px;
    background: transparent;
    color: var(--vntv-icon);
    cursor: pointer;
    transition: background-color 120ms ease, color 120ms ease;
}

.vntv-sheet-close svg {
    display: block;
}

/* A quiet word-button: Take down, Relink. Stock's tertiary
   button, the accent's words on a faint ground of it, at stock's
   32px, as the sheets' other buttons are. */
.vntv-sheet-action {
    flex: 0 0 auto;
    height: 32px;
    margin: 0;
    padding: 0 16px;
    border: none;
    border-radius: 4px;
    background: rgba(var(--vntv-accent-rgb), 0.08);
    color: var(--vntv-accent);
    cursor: pointer;
    font: inherit;
    font-size: 12px;
    font-weight: 600;
    white-space: nowrap;
    transition: background-color 120ms ease;
}

.vntv-sheet-action[aria-disabled="true"],
.vntv-sheet-action:disabled {
    cursor: default;
    opacity: 0.6;
}

.vntv-sheet-close:focus-visible,
.vntv-sheet-action:focus-visible,
.vntv-sheet-primary:focus-visible {
    outline: none;
    box-shadow: 0 0 0 2px var(--vntv-focus);
}

.vntv-sheet .vntv-input {
    box-sizing: border-box;
    height: 32px;
    padding: 0 10px;
    border-radius: 4px;
    font-size: 13px;
}

/* Stock's primary button. */
.vntv-sheet-primary {
    flex: 0 0 auto;
    height: 32px;
    margin: 0;
    padding: 0 16px;
    border: none;
    border-radius: 4px;
    background: var(--vntv-accent);
    color: var(--vntv-on-accent);
    cursor: pointer;
    font: inherit;
    font-size: 12px;
    font-weight: 600;
}

.vntv-sheet-primary:disabled {
    cursor: default;
    opacity: 0.6;
}

/* Sending a bearer token: still pressable to the keyboard and a screen
   reader, and doing nothing until the answer comes. At full strength,
   so its words (Adding..., Saving...) can be read; the cursor and the words
   say it is busy. */
.vntv-sheet-primary[aria-disabled="true"] {
    cursor: progress;
}

/* The link key: Relink's one home, always in a linked person's bar. Lit in
   the lighter red while the stream title cannot be set, which relinking is
   what fixes. */
.vntv-bar-relinktool[data-attention="true"] {
    color: var(--vntv-danger);
}

/* Let in to watch only: The same box, "Watch only" where the title
   would be, in second-rank words, and Link your stream where Save would be,
   as stock's tertiary button. */
.vntv-watchonly {
    grid-area: field;
    display: flex;
    align-items: center;
    gap: 8px;
    min-width: 0;
    height: 46px;
    padding: 13px 16px 12px;
    color: var(--vntv-second);
    font-size: 14px;
    line-height: 20px;
}

.vntv-bar-watchonly .vntv-textkey {
    grid-area: send;
    align-self: end;
    height: 32px;
    margin: 0 7px 7px 0;
    padding: 0 16px;
    border: none;
    border-radius: 4px;
    background: rgba(var(--vntv-accent-rgb), 0.08);
    color: var(--vntv-accent);
    font-family: inherit;
    font-size: 12px;
    letter-spacing: 0;
    text-transform: none;
}

.vntv-bar-watchonly .vntv-textkey:hover {
    background: rgba(var(--vntv-accent-rgb), 0.12);
    color: var(--vntv-accent);
}

.vntv-bar-watchonly .vntv-textkey[aria-expanded="true"] {
    background: rgba(var(--vntv-accent-rgb), 0.16);
    color: var(--vntv-accent);
}

.vntv-watchonly svg {
    display: block;
    flex: 0 0 auto;
}

.vntv-watchonly-word {
    white-space: nowrap;
}

/* Their stream, matched to them by username, being kept for clipping: one
   line above the watch-only line, no switch. */
.vntv-watchonly-note {
    margin: 0 0 8px;
    padding: 0 6px;
    color: var(--vntv-second);
    font-size: 12px;
    line-height: 1.45;
}

/* The bearer token bar's buttons in a sheet: Link or Save, Cancel, Unlink. */
.vntv-token-actions {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 6px;
}

/* Unlink: a destructive action, so stock's danger tertiary (Not a
   grey outline); its first press says in words what the second one does,
   on a line of its own, as the filled danger button. */
.vntv-unlink {
    background: rgba(var(--vntv-danger-rgb), 0.08);
    color: var(--vntv-danger);
    box-shadow: none;
}

.vntv-unlink[data-armed="true"] {
    flex: 1 1 100%;
    height: auto;
    min-height: 32px;
    padding: 6px 12px;
    background: var(--vntv-danger-fill);
    color: var(--vntv-on-danger);
    box-shadow: none;
    line-height: 1.35;
    text-align: left;
    white-space: normal;
}

.vntv-unlink:focus-visible {
    box-shadow: 0 0 0 2px var(--vntv-focus);
}

/* Busy, its words stay whole: they say what is happening. */
.vntv-unlink[aria-disabled="true"] {
    cursor: progress;
}

/* The planning form says which plan it is changing in the sheet's head, so
   its own line saying so is not needed twice. */
.vntv-sheet-plan .vntv-sched-editing {
    display: none;
}

/* The forms' joined fields, in a sheet: a little taller, a quieter edge and
   the sheet's softer corners, so they sit in it as its own controls. Red
   still comes up round the one being typed in. */
.vntv-sheet .vntv-field {
    min-height: 32px;
    border-color: var(--vntv-line);
    border-radius: 4px;
}

.vntv-sheet .vntv-field:focus-within {
    border-color: var(--vntv-accent);
}

/* What's on?'s picker: the planning form's first row and nothing else. */
.vntv-whatson-form {
    display: flex;
    flex-direction: column;
    gap: 6px;
}

/* Suggestions in a sheet open in the footer, which takes its height from the
   panel above it, so they are held to a share of the window rather than the
   planning form's fixed cap. */
.vntv-sheet .vntv-suggest-list {
    max-height: min(290px, 30vh);
}

/* And upward, between the sheet's head and the box, like the sheet itself:
   under the box they pushed the box up the panel as they came and went while
   it was being typed in. Moved on screen only, so they still come after the
   box for a screen reader and the keyboard; Up from the box goes to the last
   of them, which is now the nearest. Their shadow falls upward with them. */
.vntv-sheet .vntv-suggest {
    order: -1;
    box-shadow: 0 -6px 14px 0 rgba(0, 0, 0, 0.12);
}

@media (hover: hover) {
    .vntv-link-cta:hover {
        background: color-mix(in srgb, var(--vntv-accent) 90%, #000000);
    }

    .vntv-bar-tool:hover {
        background: var(--vntv-fill-hover);
        color: var(--vntv-icon-hover);
    }

    .vntv-bar-tool[aria-expanded="true"]:hover {
        background: rgba(var(--vntv-accent-rgb), 0.12);
        color: var(--vntv-accent);
    }

    .vntv-bar-save:not([aria-disabled="true"]):hover {
        background: color-mix(in srgb, var(--vntv-accent) 90%, #000000);
    }

    .vntv-sheet-action:not(:disabled):hover {
        background: rgba(var(--vntv-accent-rgb), 0.12);
    }

    /* The destructive ones deepen their own red pointed at; armed,
       they stay the filled danger button under the pointer that pressed
       them once. */
    .vntv-sheet-action.vntv-unlink:not(:disabled):hover,
    .vntv-sheet-action.vntv-account-disconnect:not(:disabled):hover,
    .vntv-sheet-action.vntv-twitch-disconnect:not(:disabled):hover {
        background: rgba(var(--vntv-danger-rgb), 0.16);
    }

    .vntv-sheet-action[data-armed="true"]:not(:disabled):hover {
        background: var(--vntv-danger-fill);
    }

    .vntv-bar-alert .vntv-sheet-action:not(:disabled):hover {
        background: rgba(var(--vntv-fg-rgb), 0.16);
    }

    .vntv-sheet-close:hover {
        background: var(--vntv-fill-hover);
        color: var(--vntv-icon-hover);
    }

    .vntv-sheet-primary:not(:disabled):hover {
        background: color-mix(in srgb, var(--vntv-accent) 90%, #000000);
    }
}

/* ---- Awards beside a name ----
   Rendered inside Mattermost's own post header and profile popover rather than
   inside the panel, so it inherits the surrounding line's alignment and adds
   nothing but the badges themselves. */
.vntv-awards {
    gap: 3px;
    vertical-align: middle;
}

/* Pulled as far left as a plugin can reach in a post header.
   Mattermost's header is two columns: col__name, which holds the username, the
   tags and the CUSTOM STATUS EMOJI that the live badge uses, and then
   badges-wrapper, which holds the timestamp. The only plugin slot in the whole
   header is inside the second one, after the timestamp — col__name has no
   extension point at all, so nothing a plugin renders can sit to the LEFT of
   the live badge. This puts the awards as close to it as the API allows:
   immediately after it and before the timestamp.
   (No backticks in here: this is inside a template literal.) */
.vntv-awards-post {
    order: -1;
    margin-right: 6px;
}

.vntv-awards svg {
    display: block;
}

/* ---- Profile popover badge ---- */
/* A column, unlike the pills it used to hold on their own: a person's planned
   streams go underneath them. .vntv-inline sets row, and this wins by coming
   later in the same stylesheet.

   color: inherit is load-bearing now. .vntv paints its own text white for the
   black panel, and until this had plain text in it that never mattered — the
   pills and the badges carry their own colour. A white line of text in a
   profile popover is invisible to anybody on a light Mattermost theme, so the
   text here takes the colour of the popover it is sitting in. */
.vntv-popover {
    display: flex;
    flex-direction: column;
    align-items: stretch;
    gap: 5px;
    padding: 5px 0;
    max-width: 260px;
    color: inherit;
}

/* One row of the profile popover: a mark on the left, a headline and a detail
   line beside it. Three things use it — a medal, being on air, and a planned
   stream — and they are the same KIND of thing: one fact about a person. They
   share these measurements rather than three copies of them, because three
   copies is how rows that are supposed to line up stop lining up.

   Only the tint and the mark change between them. */
/* A quiet fill of the popover's own words and stock's 4px corner.
   The quietest fill, and the same for every row: the medals keep
   their metals on the glyphs, not on whole rows. Only being live or in
   voice tints a row, as states. */
.vntv-factrow {
    display: flex;
    align-items: center;
    gap: 9px;
    border-radius: 4px;
    padding: 6px 9px 6px 7px;
    background: var(--vntv-fill);
}

/* Fixed width so the headlines line up down the list whatever the mark is —
   a 22px medal, a 9px dot and a 15px calendar are otherwise three indents. */
.vntv-factrow-mark {
    display: flex;
    flex: 0 0 auto;
    align-items: center;
    justify-content: center;
    width: 24px;
    color: var(--vntv-accent);
}

.vntv-factrow-text {
    display: flex;
    flex: 1 1 auto;
    flex-direction: column;
    min-width: 0;
}

.vntv-factrow-title {
    overflow: hidden;
    font-family: var(--vntv-display);
    font-weight: 600;
    font-size: 13px;
    line-height: 16px;
    letter-spacing: 0.01em;
    text-overflow: ellipsis;
    white-space: nowrap;
}

.vntv-factrow-meta {
    overflow: hidden;
    opacity: 0.7;
    font-size: 11px;
    line-height: 15px;
    text-overflow: ellipsis;
    white-space: nowrap;
}

/* Gold, silver and bronze: On the medal glyphs only, which carry
   their own metals; the rows keep the quiet fill above. */

/* The months already over, as one row: "Monthly champion", a count beside it
   from the second win, and which months underneath. The title still gives way
   first on a narrow popover; the count never does. */
.vntv-factrow-head {
    display: flex;
    align-items: center;
    gap: 6px;
    min-width: 0;
}

/* The count is the medal itself in miniature: the gold of the artwork with
   its dark ink for the digits, the same pair as in badges.tsx. That pair
   carries its own contrast, so it reads the same on a light theme and a dark
   one, where a gold figure on the popover's own colour would vanish on white. */
.vntv-factrow-count {
    flex: 0 0 auto;
    padding: 0 6px;
    border-radius: 999px;
    background: #f2c94c;
    color: #102338;
    font-family: var(--vntv-display);
    font-size: 11px;
    font-weight: 600;
    line-height: 15px;
    letter-spacing: 0.02em;
    font-variant-numeric: tabular-nums;
}

/* The detail line of that row, which wraps rather than cutting off: the
   months are the point of it, and an ellipsis would hide them. Full strength
   itself so the small badges in it are not faded; the months take the usual
   dimming on their own. */
.vntv-factrow-meta.vntv-factrow-tallies {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    column-gap: 8px;
    row-gap: 1px;
    overflow: visible;
    opacity: 1;
    white-space: normal;
}

.vntv-factrow-months {
    min-width: 0;
    opacity: 0.7;
    overflow-wrap: anywhere;
}

/* Second and third place: a small calendar and how many times. */
.vntv-factrow-tally {
    display: inline-flex;
    flex: 0 0 auto;
    align-items: center;
    gap: 2px;
    font-weight: 600;
    font-variant-numeric: tabular-nums;
    white-space: nowrap;
}

.vntv-factrow-tally svg {
    display: block;
}

/* On air outranks a medal, and looks it. In LIVE's own red. */
.vntv-factrow-live {
    background: rgba(var(--vntv-live-rgb), 0.16);
}

/* On Mumble: about now, like the live row, but quieter, and in the accent,
   as the headphones' disc is: being in voice is company, not a broadcast. */
.vntv-factrow-voice {
    background: rgba(var(--vntv-accent-rgb), 0.08);
}

.vntv-factrow-dot {
    width: 9px;
    height: 9px;
    border-radius: 50%;
    background: var(--vntv-live);
    animation: vntv-pulse 2s ease-in-out infinite;
}

/* The one thing in the popover you can press, so it keeps its edges while
   everything around it is a tinted panel. */
.vntv-factrow-action {
    flex: 0 0 auto;
}

/* The category marks are filled and already carry their own colour from the
   mark slot; this exists so they sit on the text baseline like the others. */
.vntv-factrow-kind {
    display: flex;
    align-items: center;
}

/* A plan linked to a movie, show or game: its art faded in behind the row,
   darkest where the words are — the same smoked glass as its Up next card,
   lighter here, because this sits in Mattermost's own popover on whatever
   theme the reader has chosen, light or dark. Its own stacking context, so
   the art can sit at -1: above the row's tint, beneath its words. */
.vntv-factrow[data-media="true"] {
    position: relative;
    isolation: isolate;
    overflow: hidden;
}

.vntv-factrow-art {
    position: absolute;
    inset: 0;
    z-index: -1;
    width: 100%;
    height: 100%;
    object-fit: cover;

    /* Capped as the Up next card's is, so the second-rank words over
       it keep their contrast on a light theme. */
    opacity: 0.08;

    /* Faint behind the words, and no stronger than that behind the Watch
       button at the right-hand end of a live row, which sits on it too. */
    -webkit-mask-image: linear-gradient(to right, rgba(0, 0, 0, 0.2) 0, rgba(0, 0, 0, 0.6) 45%, rgba(0, 0, 0, 0.6) 100%);
    mask-image: linear-gradient(to right, rgba(0, 0, 0, 0.2) 0, rgba(0, 0, 0, 0.6) 45%, rgba(0, 0, 0, 0.6) 100%);
    pointer-events: none;
}

/* The cover where the category icon goes: a portrait poster or box art, or
   Steam's wide capsule for a game's thumb. Wider than the icon slot, so the
   headlines of linked rows line up with each other rather than with the
   medals above. */
.vntv-factrow-mark.vntv-factrow-cover {
    position: relative;
    width: 30px;
    height: 45px;
    border-radius: 4px;
    background: var(--vntv-fill-hover);
    box-shadow: var(--elevation-2, 0 4px 6px 0 rgba(0, 0, 0, 0.12));
}

.vntv-factrow-mark.vntv-factrow-cover[data-kind="game"] {
    width: 52px;
    height: 20px;
}

.vntv-factrow-mark.vntv-factrow-cover[data-kind="game"][data-art="tall"] {
    width: 30px;
    height: 45px;
}

.vntv-factrow-cover img {
    display: block;
    width: 100%;
    height: 100%;
    border-radius: inherit;
    object-fit: cover;
}

/* On a live row the live dot rides on the cover's corner. Cut out
   of what is behind it by a ring of the popover's ground, as Mattermost's
   own status dot is. */
.vntv-factrow-cover .vntv-factrow-dot-on {
    position: absolute;
    right: -3px;
    bottom: -3px;
    box-shadow: 0 0 0 2px var(--vntv-bg);
}

/* What is on said with no title to look up: its kind where a cover would be,
   with the live dot on its corner the way a cover has it. */
.vntv-factrow-mark.vntv-factrow-onnow {
    position: relative;
}

.vntv-factrow-onnow .vntv-factrow-dot-on {
    position: absolute;
    right: -1px;
    bottom: -4px;
    width: 7px;
    height: 7px;
    box-shadow: 0 0 0 2px var(--vntv-bg);
}

.vntv-factrow-media {
    overflow: hidden;
    font-size: 11px;
    font-weight: 600;
    line-height: 15px;
    opacity: 0.9;
    text-overflow: ellipsis;
    white-space: nowrap;
}

/* Over a title's art, the Watch button stands on the popover's own surface —
   the theme's ground, whatever the theme — so it reads at the contrast the
   theme gives it anywhere else, however bright or busy the art behind is.
   The pill's own accent tints over it. */
.vntv-factrow[data-media="true"] .vntv-factrow-action {
    background: linear-gradient(rgba(var(--vntv-accent-rgb), 0.08), rgba(var(--vntv-accent-rgb), 0.08)), var(--vntv-bg);
}

.vntv-factrow[data-media="true"] .vntv-factrow-action:hover {
    background: linear-gradient(rgba(var(--vntv-accent-rgb), 0.16), rgba(var(--vntv-accent-rgb), 0.16)), var(--vntv-bg);
}

/* The awards, spelled out. One row per medal: the artwork, the place in words,
   and which board it came from underneath.

   Rendered inside Mattermost's own profile popover rather than in the panel, so
   nothing here picks a text colour — it inherits, and reads on the light and
   dark themes alike. The tints on .vntv-factrow are the medal's own colour at
   low alpha, which works on both for the same reason. */
.vntv-awardlist {
    display: flex;
    flex-direction: column;
    gap: 4px;
    margin: 0;
    padding: 0;
    list-style: none;
}

.vntv-popover-plans {
    display: flex;
    flex-direction: column;
    gap: 4px;
    margin: 0;
    padding: 0;
    list-style: none;
}

/* A planned stream is a .vntv-factrow like the other two. Nothing left here but
   the list it sits in, above. */

/* ---- 3.4.0: Steam and Hytale in the profile popover ----
   design/3.4.0.md 5.4, with Victor's Q8 pick (AccountRows.tsx): two more
   factrows, after On Mumble and before the awards, on the popover's plain
   grey tint, since they are facts about a person rather than the panel's
   own news, as being live or in voice is. No Steam or Hytale mark: a plain
   controller and a plain cube, stroked like the bar's tools.

   Offline, private and not read lately are one thing to everybody else:
   the mark and the name fade rather than turn grey, which reads the same on
   a light theme and a dark one. The meta line is already at 0.7 and its one
   word, Steam, says what the row is, so it is never taken for Mattermost's
   status dot: it does not fade again. Nor does the profile pill, which
   still works. */
.vntv-factrow[data-dim="true"] .vntv-factrow-mark,
.vntv-factrow[data-dim="true"] .vntv-factrow-title {
    opacity: 0.6;
}

/* A Hytale name is typed into a game letter for letter, so one press on it
   takes the whole name, for a reader whose clipboard is refused. */
.vntv-factrow-copyable {
    user-select: all;
}

/* The two new pills: the Steam profile link, a square icon pill, and Copy.
   Mattermost's colour and underline for links are overruled, as for Watch
   on Twitch.

   They are pills like Watch beside them, so they take the pill's
   accent, which the theme makes readable on its own ground: the lighter red
   a dark theme needed for the plugin's red is gone with it. */
.vntv-popover a.vntv-account-pill,
.vntv-popover a.vntv-account-pill:hover,
.vntv-popover a.vntv-account-pill:focus-visible {
    color: var(--vntv-accent);
    text-decoration: none;
}

.vntv-popover .vntv-account-pill:hover {
    background: rgba(var(--vntv-accent-rgb), 0.16);
}

.vntv-popover .vntv-account-pill:focus-visible {
    outline: none;
    box-shadow: 0 0 0 2px var(--vntv-focus);
}

/* Copied stands where Copy was for two seconds, filled, so the change is
   seen without reading it: the theme's words on its accent, as its primary
   buttons are. */
.vntv-popover .vntv-account-pill.vntv-copy-pill[data-copied="true"] {
    background: var(--vntv-accent);
    color: var(--vntv-on-accent);
}

/* Copy and Copied in one grid cell, so the pill is always as wide as
   Copied: the name beside it never narrows, or cuts, for the two seconds
   Copied shows. The hidden word is visibility: hidden, out of sight and out
   of the button's name, which says it in full. */
.vntv-copy-faces {
    display: grid;
    justify-items: center;
}

.vntv-copy-face {
    grid-area: 1 / 1;
}

.vntv-copy-face:not([data-shown="true"]) {
    visibility: hidden;
}

/* ---- The side panel's width ----
   The only rules here that reach outside the plugin's own elements, and only
   while the panel is open in the side panel in a window wide enough for
   rhsWidth.ts to be widening it. That sets three variables on body:
   --overrideRhsWidth, the width Mattermost sizes its side panel and the
   spacer beside it from; --vntv-rhs-max, the widest it may go in this window
   (never past 1140px, where the panel's own content stops); and
   --vntv-rhs-expanded, the width of Mattermost's expanded view.

   The first rule lifts Mattermost's own cap to that widest — without it the
   panel stops at 776px however wide the window — and sets the width too, as
   Mattermost's own rule does. The moment after leaving the expanded view,
   while Mattermost lets the width settle, is its own.

   The second is the expanded view. Mattermost draws it at 60 percent of the
   window but never past 960px, while the spacer beside it keeps this panel's
   width, so where the panel is wider than that, expanding made it NARROWER
   and left a strip of nothing between it and the channel. Here it is never
   narrower than the panel, and no wider than the content. Below 1280px,
   Mattermost's own tablet rule stands. */
body.vntv-rhs-wide .sidebar--right:not(.expanded):not(.resize-disabled) {
    width: var(--overrideRhsWidth);
    max-width: var(--vntv-rhs-max, 776px);
}

@media (min-width: 1280px) {
    body.vntv-rhs-wide .sidebar--right.sidebar--right--expanded {
        width: var(--vntv-rhs-expanded, min(60vw, 96rem));
    }
}

/* And Mattermost's own close button, hidden whenever this panel is open,
   at every size, as asked: the panel is opened and closed with the plugin's
   button — in the App Bar, or in the channel header where there is no App
   Bar — or Ctrl+. (Cmd+. on a Mac). */
body.vntv-rhs-open #sidebar-right .sidebar--right__close {
    display: none;
}

/* And Mattermost's own Expand button in the panel's header, hidden while
   expanding would add nothing: the panel already as wide as the expanded view
   would make it, which on a window of about 2230px or more it always is
   (rhsWidth.ts sets vntv-rhs-full). Where expanding would still widen it, the
   button stays. Only while the panel is not expanded, too: one expanded some
   other way (Ctrl+Shift+.) keeps the button that collapses it. */
body.vntv-rhs-wide.vntv-rhs-full #sidebar-right:not(.sidebar--right--expanded) .sidebar--right__expand {
    display: none;
}

/* ---- Coming back to the panel ----
   The panel takes focus after it comes back by itself following a reply
   (away.ts), so a stray key lands somewhere harmless and one Tab reaches Back
   to thread. A ring only when the keyboard put it there. */
.vntv-panel:focus {
    outline: none;
}

.vntv-panel:focus-visible {
    outline: 2px solid var(--vntv-focus);
    outline-offset: -2px;
}

/* The strip at the top of the panel after such a return: what happened, and
   the way back to the thread (ReturnStrip.tsx). */
.vntv-return {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 8px 10px;
    border: 1px solid var(--vntv-line);
    border-left: 3px solid var(--vntv-accent);
    border-radius: 4px;
    background: var(--vntv-bg);
    animation: vntv-return-in ${MINI_FADE_MS}ms ease-out backwards;
}

.vntv-return-text {
    flex: 1 1 auto;
    min-width: 0;
    color: var(--vntv-fg);
    font-size: 12px;
}

@keyframes vntv-return-in {
    from { opacity: 0; transform: translate3d(0, -4px, 0); }
    to { opacity: 1; transform: none; }
}

/* ---- The small player ----
   The stream that was in front, in a corner of the channel, while something
   else has the panel's place in the side panel (MiniPlayer.tsx). Fixed to the
   window and placed through three variables MiniPlayer.tsx sets: above the
   message list and its toasts, beneath the side panel (20), Mattermost's
   menus and its dialogs.

   A .vntv root of its own, so it gets the panel's colours and type, but not
   its height or its overflow: it is as tall as what is in it.

   A stock floating surface, like a popover: the theme's ground and
   words, a 1px line and stock elevation. The line is drawn as a shadow, so
   it takes no room from the sums in miniLayout.ts. */
.vntv.vntv-mini {
    position: fixed;
    top: 0;
    left: 0;
    z-index: 19;
    width: var(--vntv-mini-w, 240px);
    height: auto;
    transform: translate3d(var(--vntv-mini-x, 0), var(--vntv-mini-y, 0), 0);
    border: 0;
    border-radius: 4px;
    background: var(--vntv-bg);
    color: var(--vntv-fg);
    box-shadow: 0 0 0 1px var(--vntv-line), var(--elevation-4, 0 8px 24px 0 rgba(0, 0, 0, 0.12));
    touch-action: none;
    user-select: none;
    animation: vntv-mini-in ${MINI_FADE_MS}ms ease-out backwards;
}

/* It grows out of its own corner. The scale is the separate property, so it
   does not fight the translate that places it. */
.vntv-mini[data-corner="tr"] {
    transform-origin: top right;
}

.vntv-mini[data-corner="tl"] {
    transform-origin: top left;
}

.vntv-mini[data-corner="br"] {
    transform-origin: bottom right;
}

.vntv-mini[data-corner="bl"] {
    transform-origin: bottom left;
}

@keyframes vntv-mini-in {
    from { opacity: 0; scale: 0.92; }
    to { opacity: 1; scale: 1; }
}

/* A change of size fades the new one in. Two names for the same fade, swapped
   on each change, because an animation only plays again when its name
   changes; remounting to replay it would take the picture out of the page. */
.vntv-mini[data-flip="a"] {
    animation: vntv-mini-swap-a ${MINI_FADE_MS}ms ease-out backwards;
}

.vntv-mini[data-flip="b"] {
    animation: vntv-mini-swap-b ${MINI_FADE_MS}ms ease-out backwards;
}

@keyframes vntv-mini-swap-a {
    from { opacity: 0.35; }
    to { opacity: 1; }
}

@keyframes vntv-mini-swap-b {
    from { opacity: 0.35; }
    to { opacity: 1; }
}

/* The panel is back in the side panel and has taken the picture back: the player
   fades out where it is. */
.vntv-mini[data-leaving="true"] {
    pointer-events: none;
    animation: vntv-mini-out ${MINI_FADE_MS}ms ease-in forwards;
}

@keyframes vntv-mini-out {
    from { opacity: 1; scale: 1; }
    to { opacity: 0; scale: 0.92; }
}

/* Settling into a corner after a drag or a Move. */
.vntv-mini[data-snapping="true"] {
    transition: transform ${SNAP_MS}ms cubic-bezier(0.2, 0.8, 0.2, 1);
}

/* While it is dragged its controls are out of the way. */
.vntv-mini[data-dragging="true"] {
    cursor: grabbing;
}

.vntv-mini[data-dragging="true"] .vntv-hud {
    display: none;
}

/* A file being dragged over the window goes through it, to the channel
   underneath, which is where Mattermost takes uploads. */
.vntv-mini[data-file-drag="true"] {
    opacity: 0.3;
    pointer-events: none;
}

/* The picture: a 16:9 screen as wide as the player, with none of the tile
   grid's height caps. */
.vntv-mini-screen {
    position: relative;
    flex: 0 0 auto;
    cursor: grab;
}

.vntv-mini .vntv-screen {
    max-width: none;
    max-height: none;

    /* The small player's own frame is the one shape: its picture runs to
       its edges, and its corners are the frame's. */
    border-radius: 0;
}

/* Kept to one line: the plate is centred from the middle of a small screen,
   which leaves its width at most half of it, and NO SIGNAL would otherwise
   break in two whenever the note under it is short. */
.vntv-mini .vntv-plate-title {
    white-space: nowrap;
}

/* The line under it: Back to (the panel) and whose stream it is. First in the
   DOM, so Back is the first stop for the keyboard; under the picture on
   screen. */
.vntv-mini-bar {
    display: flex;
    align-items: center;
    gap: 2px;
    height: 32px;
    min-width: 0;
    padding: 0 2px;
    cursor: grab;
}

.vntv-mini[data-mode="picture"] .vntv-mini-bar {
    order: 2;
}

.vntv-mini .vntv-player-bar {
    flex: 1 1 auto;
    min-width: 0;
    min-height: 0;
    padding: 0 6px 0 2px;
}

.vntv-mini .vntv-player-name {
    font-size: 13px;
}

/* Back to the panel: the panel's own icon, a stock icon button sized to
   the 32px line it sits in. */
.vntv-mini-back {
    display: inline-flex;
    flex: 0 0 auto;
    align-items: center;
    justify-content: center;
    width: 28px;
    height: 28px;
    padding: 0;
    border: 0;
    border-radius: 4px;
    background: transparent;
    cursor: pointer;
    line-height: 0;
    transition: background-color 120ms ease;
}

.vntv-mini-back img {
    display: block;
    width: 20px;
    height: 20px;
}

.vntv-mini-back:hover {
    background: var(--vntv-fill-hover);
}

.vntv-mini-back:focus-visible {
    outline: none;
    box-shadow: 0 0 0 2px var(--vntv-focus);
}

/* How many other streams are being watched, drawn as the Live now count
   draws its number but in the accent, as WATCHING is: these are streams
   being watched, not a state of being live. Centred as a grid, so the digit sits in the middle whatever the
   font's own side bearings. Not a control: the count is read out from the
   region's description instead. */
.vntv-mini-more {
    position: absolute;
    top: 6px;
    right: 6px;
    z-index: 2;
    display: inline-grid;
    place-items: center;
    box-sizing: border-box;
    min-width: 16px;
    height: 16px;
    padding: 0 4px;
    border-radius: 999px;
    background: var(--vntv-accent);
    color: var(--vntv-on-accent);
    font-size: 10px;
    line-height: 1;
    font-variant-numeric: tabular-nums;
    pointer-events: none;
}

/* Turn sound on, and Retry: pills over the picture, above its controls and
   the tube's light, on a dark ground so they read over anything. On
   the picture, so white words and a white line on the scrim on every
   theme, never the accent (Denim's blue on black is unreadable). */
.vntv-mini-sound,
.vntv-mini-retry {
    position: absolute;
    top: 6px;
    left: 6px;
    z-index: 5;
    border-color: rgba(255, 255, 255, 0.5);
    background: rgba(0, 0, 0, 0.78);
    color: #ffffff;
}

.vntv-mini button.vntv-mini-sound:hover,
.vntv-mini button.vntv-mini-retry:hover {
    background: rgba(0, 0, 0, 0.9);
}

.vntv-mini button.vntv-mini-sound:focus-visible,
.vntv-mini button.vntv-mini-retry:focus-visible {
    border-color: #ffffff;
    box-shadow: 0 0 0 2px rgba(0, 0, 0, 0.6);
}

/* The controls on the picture, sized down for it. */
.vntv-mini .vntv-hud-power {
    width: 44px;
    height: 44px;
    margin: -22px 0 0 -22px;
}

.vntv-mini .vntv-hud-bar {
    gap: 2px;
    padding: 18px 6px 4px;
}

.vntv-mini-move,
.vntv-mini-hide {
    flex: 0 0 auto;
}

/* Bar mode's own row of controls; the picture has them on it instead. */
.vntv-mini-controls {
    display: flex;
    flex: 0 0 auto;
    align-items: center;
    padding: 0 4px 0 0;
}

.vntv-mini[data-mode="picture"] .vntv-mini-controls {
    display: none;
}

.vntv-mini-controls .vntv-hud-button {
    width: 28px;
    height: 28px;
}

/* In bar mode these sit on the player's own ground, not on the
   picture, so they are stock icon buttons: the glyph at the icon opacity, a
   quiet ground pointed at, and the stock ring. Two classes and the root's,
   to outrank the picture's white buttons. */
.vntv-mini .vntv-mini-controls .vntv-hud-button {
    border-radius: 4px;
    color: var(--vntv-icon);
}

.vntv-mini .vntv-mini-controls .vntv-hud-button:hover {
    background: var(--vntv-fill-hover);
    color: var(--vntv-icon-hover);
}

.vntv-mini .vntv-mini-controls .vntv-hud-button:focus-visible {
    background: var(--vntv-fill-hover);
    color: var(--vntv-icon-hover);
    box-shadow: 0 0 0 2px var(--vntv-focus);
}

/* What the No Signal plate says, in a word, under the name in bar mode, where
   the plate does not fit (MiniPlayer.tsx). Picture mode has the plate. */
.vntv-mini-state {
    display: none;
}

/* Bar mode: one row, where there is no room for the picture. The picture is
   a thumbnail at its start, still the same element in the page, so the sound
   carries on; nothing goes over it, and the power switch is left to picture
   mode. */
.vntv-mini[data-mode="bar"] {
    flex-direction: row;
    align-items: stretch;
    height: 44px;
}

.vntv-mini[data-mode="bar"] .vntv-mini-screen {
    order: -1;
    flex: 0 0 56px;
    width: 56px;
}

/* 56px rather than the 78 that 16:9 would take at this height, which left
   the name about 28px, too few for even a short one. The picture fills the
   thumbnail and loses a little at its sides, rather than shrinking into a
   band across the middle. */
.vntv-mini[data-mode="bar"] .vntv-video {
    object-fit: cover;
}

.vntv-mini[data-mode="bar"] .vntv-mini-screen .vntv-screen {
    height: 100%;
    aspect-ratio: auto;
}

/* At 240 to 360px the bar has room for a name and no more: the face, what
   is on and the stream title (and any links in it) are left to picture mode,
   and the name takes whatever is left, with an ellipsis. Measured: with them,
   the name came out no pixels wide. */
.vntv-mini[data-mode="bar"] .vntv-hud,
.vntv-mini[data-mode="bar"] .vntv-mini-more,
.vntv-mini[data-mode="bar"] .vntv-mini-sound,
.vntv-mini[data-mode="bar"] .vntv-mini-retry,
.vntv-mini[data-mode="bar"] .vntv-plate,
.vntv-mini[data-mode="bar"] .vntv-player-face,
.vntv-mini[data-mode="bar"] .vntv-player-onnow,
.vntv-mini[data-mode="bar"] .vntv-player-note {
    display: none;
}

.vntv-mini[data-mode="bar"] .vntv-player-name {
    max-width: none;
}

.vntv-mini[data-mode="bar"] .vntv-player-bar {
    padding: 0 4px 0 2px;
}

.vntv-mini[data-mode="bar"] .vntv-mini-bar {
    flex: 1 1 auto;
    height: auto;
}

/* With a word under the name, the name line is two lines beside Back. */
.vntv-mini[data-mode="bar"] .vntv-mini-bar[data-state="true"] {
    display: grid;
    grid-template-columns: 28px minmax(0, 1fr);
    grid-template-rows: auto auto;
    align-content: center;
    gap: 0 2px;
}

.vntv-mini[data-mode="bar"] .vntv-mini-bar[data-state="true"] .vntv-mini-back {
    grid-row: 1 / span 2;
}

.vntv-mini[data-mode="bar"] .vntv-mini-state {
    display: block;
    overflow: hidden;
    padding: 0 2px;
    color: var(--vntv-second);
    font-family: var(--vntv-display);
    font-size: 10px;
    line-height: 13px;
    letter-spacing: 0.08em;
    text-overflow: ellipsis;
    text-transform: uppercase;
    white-space: nowrap;
}

/* Draining from the right while the panel is about to come back after a
   reply, over exactly the time it waits. */
.vntv-mini-drain {
    position: absolute;
    top: 0;
    right: 0;
    left: 0;
    z-index: 6;
    height: 2px;
    background: var(--vntv-accent);
    transform-origin: left center;
    animation: vntv-mini-drain ${RETURN_DELAY_MS}ms linear both;
    pointer-events: none;
}

@keyframes vntv-mini-drain {
    from { transform: scaleX(1); }
    to { transform: scaleX(0); }
}

/* ---- Lock screen ----
   What the panel is until the reader unlocks it with their bearer token, or
   an admin lets them in (design/lock-screen.md 4.2 and 4.7). The panel's own
   look, and the panel itself looking locked: black, the emblem large and
   dimmed with a padlock on it, the plugin's red, and the black bar for the
   token docked at the foot, where the unlocked panel's bar will be. Over the
   bar a status line, black until it has something to say, and nothing else
   unless there is a reason: no way in for people with no token, and nothing
   only a system admin reads (the amendment of 2026-10-06). The middle
   is one column, centred when it fits; when the side panel is short it
   scrolls FROM THE TOP, and the dock stays put under it. A flex column
   centred with justify-content would overflow above the top as well as
   below, out of reach of any scrolling, so the stage starts at the top and
   the column's own auto margins do the centring, collapsing to nothing when
   there is no room. */
.vntv-panel.vntv-lock::after {
    content: none;
}

.vntv-lock-stage {
    flex: 1 1 auto;
    min-height: 0;
    overflow-y: auto;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: flex-start;
    padding: 32px 16px 24px;
    container: vntv-lock / inline-size;
}

.vntv-lock-column {
    margin-block: auto;
    width: 100%;
    max-width: 320px;
    display: flex;
    flex-direction: column;
    align-items: center;
}

/* The panel's emblem, as an admin may have uploaded it: the whole of the
   middle, so the corner watermark is off here. Sized to the panel within
   limits (a container unit, on the stage, so it follows the side panel
   however narrow it is dragged; the plain width is for a browser without
   them). Dimmed, as if behind glass, with the padlock on it at full
   strength; the glass clears as the lock opens. Dimmed by about half: much
   more, and the plugin's red ring went a muddy maroon. */
.vntv-lock-mark {
    position: relative;
    flex: 0 0 auto;
    width: 160px;
    width: clamp(128px, 46cqi, 208px);
    aspect-ratio: 1;
}

.vntv-lock-emblem {
    width: 100%;
    height: 100%;
    background: var(--vntv-mark) center / contain no-repeat;
    opacity: 0.55;
    transition: opacity 160ms ease-out;
}

.vntv-lock[data-unlocking="true"] .vntv-lock-emblem {
    opacity: 0.92;
}

.vntv-lock[data-state="starting"] .vntv-lock-emblem {
    opacity: 0.25;
}

/* The first moment of starting is plain black, so a fast answer shows no
   emblem at all. */
.vntv-lock[data-dark="true"] .vntv-lock-emblem {
    visibility: hidden;
}

/* The padlock on the emblem, where a status sits on a picture: across its
   edge at the lower right, ringed in the plugin's red, on black with a black
   moat so it reads over any emblem an admin uploads. Undimmed. */
.vntv-lock-badge {
    position: absolute;
    top: 85%;
    left: 85%;
    display: flex;
    align-items: center;
    justify-content: center;
    box-sizing: border-box;
    width: clamp(34px, 24%, 46px);
    aspect-ratio: 1;
    border: 2px solid var(--vntv-accent);
    border-radius: 50%;
    background: var(--vntv-bg);
    color: var(--vntv-fg);
    box-shadow: 0 0 0 4px var(--vntv-bg);
    transform: translate(-50%, -50%);
}

/* A server with no emblem: the lock is the padlock alone, in the middle. */
.vntv-lock:not([data-emblem="true"]) .vntv-lock-mark {
    width: 72px;
}

.vntv-lock:not([data-emblem="true"]) .vntv-lock-badge {
    top: 50%;
    left: 50%;
    width: 100%;
}

.vntv-lock-badge svg {
    display: block;
    flex: 0 0 auto;
    overflow: visible;
}

/* The padlock's shackle swings open about its right leg as the lock opens. */
.vntv-lock-shackle {
    transform-box: view-box;
    transform-origin: 16px 10px;
    transition: transform 180ms ease-out;
}

.vntv-lock[data-unlocking="true"] .vntv-lock-shackle {
    transform: translateY(-2px) rotate(-28deg);
}

/* Why the lock is up, when there is a reason, and the not-set-up sentence:
   one quiet line under the emblem. */
.vntv-lock-line {
    margin: 22px 0 0;
    max-width: 300px;
    color: var(--vntv-second);
    font-size: 13px;
    line-height: 1.5;
    text-align: center;
}

.vntv-lock-retry {
    margin-top: 10px;
}

/* Disconnect Twitch where there is no bar to sit beside (the plugin not
   set up): centred under the words, like them. */
.vntv-lock-column .vntv-twitch-lock {
    align-items: center;
    margin-top: 16px;
    text-align: center;
}

.vntv-lock-column .vntv-twitch-lock .vntv-textkey {
    margin-left: 0;
}

/* The bearer token bar, on the lock and in the footer's sheets alike. */
.vntv-token {
    display: flex;
    flex-direction: column;
    gap: 8px;
    width: 100%;
    min-width: 0;
}

.vntv-lock-label {
    color: var(--vntv-second);
    font-family: var(--vntv-display);
    font-size: 11px;
    font-weight: 600;
    letter-spacing: 0.14em;
    text-transform: uppercase;
}

/* In a sheet, stock's text field: a 1px line, darker when pointed
   at, the accent round it while it is typed in, the theme's error words'
   colour round it after a refusal. */
.vntv-lock-bar {
    position: relative;
    display: flex;
    align-items: center;

    /* The sheets' one field height, as the name, emoji and plan
       fields are. */
    height: 32px;
    background: var(--vntv-bg);
    border: 1px solid var(--vntv-line);
    border-radius: 4px;
    transition: border-color 120ms ease, box-shadow 120ms ease;
}

.vntv-lock-bar:hover {
    border-color: rgba(var(--vntv-fg-rgb), 0.48);
}

.vntv-lock-bar:focus-within {
    border-color: var(--vntv-accent);
    box-shadow: inset 0 0 0 1px var(--vntv-accent);
}

.vntv-lock-bar[data-error="true"] {
    border-color: var(--vntv-danger);
}

.vntv-lock-input {
    flex: 1 1 auto;
    min-width: 0;
    height: 100%;
    margin: 0;
    padding: 0 12px;
    background: transparent;
    border: 0;
    outline: none;
    color: var(--vntv-fg);
    font: 14px ui-monospace, 'SFMono-Regular', Menlo, Consolas, monospace;
    letter-spacing: 0.04em;
}

.vntv-lock-input::placeholder {
    color: var(--vntv-placeholder);
    opacity: 1;
}

/* The words asking for a token in the page's own letters, as in the
   dock; the token itself, once typed, in the monospace. */
.vntv-lock-input:placeholder-shown {
    font-family: inherit;
    letter-spacing: 0;
}

/* Masked without being a password field (LockScreen.tsx says why). */
.vntv-lock-input[data-masked="true"] {
    -webkit-text-security: disc;
}

.vntv-lock-input[readonly] {
    color: var(--vntv-second);
}

.vntv-lock-reveal {
    flex: 0 0 auto;
    height: 28px;
    margin: 0 5px 0 0;
    padding: 0 10px;
    background: transparent;
    border: none;
    border-radius: 4px;
    color: var(--vntv-second);
    cursor: pointer;
    font: inherit;
    font-size: 12px;
    font-weight: 600;
}

.vntv-lock-reveal:hover {
    background: var(--vntv-fill-hover);
    color: var(--vntv-fg);
}

.vntv-lock-reveal:focus-visible {
    outline: none;
    box-shadow: 0 0 0 2px var(--vntv-focus);
}

/* The one sentence that says what a bearer token is: readable, not muted. */
.vntv-lock-hint {
    margin: 0;
    color: var(--vntv-second);
    font-size: 11px;
    line-height: 1.45;
}

.vntv-lock-status {
    min-height: 18px;
    margin: 0;
    color: var(--vntv-second);
    font-size: 12px;
    line-height: 18px;
}

.vntv-lock-status[data-tone="error"] {
    color: var(--vntv-danger);
}

/* In a footer sheet an empty status line takes no room: the sheet would
   otherwise end in a blank stripe under its buttons. Still in the page, so
   what is put in it is announced. */
.vntv-sheet .vntv-lock-status:empty {
    min-height: 0;
    margin-top: -8px;
}

/* The lock's bar, docked at the panel's foot like a message box, where the
   unlocked panel's own bar will be: the footer's padding, height and
   corners, in the lock's black. Over it the status line, the whole width.
   Disconnect Twitch, when there is one, has a line of its own under the bar:
   beside the words over the bar it took their width, and in a narrow panel
   broke them into a column that grew the dock and moved the emblem. What
   Twitch says once that is pressed gets a row at the top, the whole width,
   since it can run to a paragraph. A container, so the words over the bar
   can keep their room by the dock's own width. */
/* The panel's footer's place, to the pixel: 24px in from the
   panel's edges and a 24px strip under the bar, which is the bottom banner
   when there is one. Disconnect, when there is one, goes over the status
   line: nothing but the strip is under the bar. */
.vntv-lock-dock {
    flex: 0 0 auto;
    padding: 0 24px 24px;
    container: vntv-lock-dock / inline-size;
}

.vntv-lock-dock[data-strip="banner"] {
    padding-bottom: 0;
}

.vntv-token.vntv-token-dock {
    display: grid;
    grid-template-columns: minmax(0, 1fr);
    grid-template-areas: "note" "key" "say" "bar";
    gap: 0;
}

/* The status line's place over the bar, its words sitting on the bar. Two
   lines high at the least (with its padding), kept while it says nothing,
   which is only black: so a status of two lines, as a refusal with the hint
   is in a side panel of ordinary width, moves nothing above it, and the
   emblem never jumps. Words with no spaces in them, a server's own, break
   rather than running out of the panel. */
.vntv-lock-say {
    grid-area: say;
    display: grid;
    align-items: end;
    min-width: 0;
    min-height: 44px;
    padding: 0 6px 8px;
    overflow-wrap: anywhere;
}

/* In a narrow side panel the longest of the lock's own words, the refusal
   with the hint, run to three lines: three are kept there. Narrow is set
   well wide of where they wrap in an Arial-sized font (296px), since a
   reader's own system font can be a sixth wider. Only words longer still, a
   server's own for a refusal this build has none for, grow the dock. */
@container vntv-lock-dock (max-width: 359px) {
    .vntv-lock-say {
        min-height: 62px;
    }
}

/* The token bar is the stream bar's twin in the same place: the
   field in the 46px top row, and under it the padlock (its label) and Show
   on the left, Unlock at the right as stock's primary button. */
.vntv-token-dock .vntv-lock-bar {
    grid-area: bar;
    display: grid;
    grid-template-columns: auto auto minmax(0, 1fr) auto;
    grid-template-rows: 46px 48px;
    grid-template-areas: "input input input input" "pad show gap go";
    align-items: center;
    box-sizing: border-box;
    height: 98px;
    gap: 0;
    padding: 0;
    border: 2px solid var(--vntv-line);
    border-radius: 4px;
    box-shadow: none;
}

.vntv-token-dock .vntv-lock-bar:hover {
    border-color: var(--vntv-line-hover);
}

.vntv-token-dock .vntv-lock-bar:focus-within,
.vntv-token-dock .vntv-lock-bar:focus-within:hover {
    border-color: var(--vntv-line-focus);
    box-shadow: none;
}

.vntv-token-dock .vntv-lock-bar[data-error="true"] {
    border-color: var(--vntv-danger);
}

.vntv-token-dock .vntv-lock-input {
    grid-area: input;
    align-self: stretch;
    height: 46px;
    padding: 13px 16px 12px;
    line-height: 20px;
}

/* The words asking for a token in the panel's own letters; the token itself,
   once there is one, in the bar's monospace. */
.vntv-token-dock .vntv-lock-input:placeholder-shown {
    font-family: inherit;
    letter-spacing: 0;
}

/* The padlock at the head of the bar, which is its label: grey, and red
   while the bar is being typed in, as the bar's edge is. */
.vntv-lock-pad {
    grid-area: pad;
    display: flex;
    flex: 0 0 auto;
    align-items: center;
    justify-content: center;
    width: 32px;
    height: 32px;
    margin: 0 0 0 7px;
    color: var(--vntv-icon);
    cursor: text;
}

.vntv-lock-pad svg {
    display: block;
    overflow: visible;
}

.vntv-lock-bar:focus-within .vntv-lock-pad,
.vntv-lock[data-unlocking="true"] .vntv-lock-pad {
    color: var(--vntv-icon-hover);
}

/* Show, inside the bar: the formatting row's own 32px button. */
.vntv-lock-tool {
    grid-area: show;
    display: flex;
    flex: 0 0 auto;
    align-items: center;
    justify-content: center;
    box-sizing: border-box;
    width: 32px;
    height: 32px;
    margin: 0 0 0 2px;
    padding: 0;
    border: 0;
    border-radius: 4px;
    background: transparent;
    color: var(--vntv-icon);
    cursor: pointer;
    transition: background-color 120ms ease, color 120ms ease;
}

.vntv-lock-tool svg {
    display: block;
}

.vntv-lock-tool:hover {
    background: var(--vntv-fill-hover);
    color: var(--vntv-icon-hover);
}

.vntv-lock-tool:focus-visible {
    outline: none;
    box-shadow: 0 0 0 2px var(--vntv-focus);
}

/* Unlock: stock's primary button, 32px tall, where the message box has
   Send, 7px off the box's bottom and right as Send is; quiet, as Send is,
   while there is nothing to send, the accent once there is and while it is
   sent. */
.vntv-lock-tool.vntv-lock-go {
    grid-area: go;
    align-self: end;
    width: auto;
    min-width: 48px;
    margin: 0 7px 7px 0;
    padding: 0 16px;
    background: var(--vntv-fill-hover);
    color: rgba(var(--vntv-fg-rgb), 0.32);
    font: inherit;
    font-size: 12px;
    font-weight: 600;
    line-height: 16px;
    white-space: nowrap;
}

.vntv-lock-go[data-ready="true"] {
    background: var(--vntv-accent);
    color: var(--vntv-on-accent);
}

.vntv-lock-go[data-ready="true"]:hover {
    background: color-mix(in srgb, var(--vntv-accent) 90%, #000000);
    color: var(--vntv-on-accent);
}

.vntv-lock-go[aria-disabled="true"] {
    cursor: progress;
}

.vntv-lock-go[data-done="true"] {
    cursor: default;
}

/* Disconnect Twitch on its own line under the bar, at the right, like a
   footnote: smaller words than a text key elsewhere, on a key still tall
   enough to press, its padding hung outside the bar's right edge. It takes
   half of the dock's padding under it, and armed, its box stands as far
   from the bar as from the banner. Its note goes in the row at the top (see
   the dock). */
.vntv-token-dock .vntv-twitch-lock {
    display: contents;
}

.vntv-token-dock .vntv-twitch-lock .vntv-twitch-disconnect {
    grid-area: key;
    justify-self: end;
    height: 32px;
    margin: 0 -4px 4px 0;
    padding: 0 6px;
    color: var(--vntv-second);
    font-size: 10px;
}

.vntv-token-dock .vntv-twitch-lock .vntv-twitch-disconnect:hover,
.vntv-token-dock .vntv-twitch-lock .vntv-twitch-disconnect[data-armed="true"] {
    color: var(--vntv-fg);
}

/* Armed, it has a box: square with the bar's edge, not hung past it. */
.vntv-token-dock .vntv-twitch-lock .vntv-twitch-disconnect[data-armed="true"] {
    margin-right: 0;
}

.vntv-token-dock .vntv-twitch-lock .vntv-twitch-note {
    grid-area: note;
    padding: 0 6px;
    font-size: 12px;
    line-height: 1.5;
}

.vntv-token-dock .vntv-twitch-note:not(:empty) {
    margin-bottom: 8px;
}

/* The lock opening: the padlocks swing open and the glass over the emblem
   clears, and then the whole of it fades and settles a little, as the panel
   takes its place. */
.vntv-lock[data-unlocking="true"] .vntv-lock-stage,
.vntv-lock[data-unlocking="true"] .vntv-lock-dock {
    animation: vntv-lock-out 260ms ease-in 160ms both;
}

@keyframes vntv-lock-out {
    from { opacity: 1; transform: scale(1); }
    to { opacity: 0; transform: scale(0.98); }
}

/* A short window: less emblem, and less around it. */
@media (max-height: 620px) {
    .vntv-lock-mark {
        width: 104px;
    }

    .vntv-lock-line {
        margin-top: 14px;
    }
}

/* A phone's browser zooms into a field whose words are under 16px. */
@media (max-width: 767px) {
    .vntv-lock-input {
        font-size: 16px;
    }
}

/* ---- Clips: the dialog ----
   The Clips dialog is the Trophies card's kind of card, a portal on the
   page: the list of clips, the drafts, the ends of streams still held, and one
   clip played. A fixed height for the list and the viewer, so going between
   them, or the list arriving, does not move the card under the pointer; the
   call to link is short, and the card is too. */
.vntv-modal-card.vntv-clips {
    max-width: 920px;
    height: min(860px, 100%);
}

.vntv-modal-card.vntv-clips[data-view="locked"] {
    max-width: 520px;
    height: auto;
}

/* The card takes focus only by script, when what had it went away, or from
   a click on its background: no ring round the whole dialog for that. */
.vntv-modal-card.vntv-clips:focus {
    outline: none;
}

/* The Clips dialog and the clip editor are stock modals, on the page
   rather than in the panel: the skin's ground and words reach their roots
   here, the page behind is dimmed as stock dims it, the card has stock's
   12px corner, a 1px line and the highest elevation, and its head is the
   stock head: the line under it, the words, a 32px close button. The
   leaderboard's own card is not touched by these. */
.vntv-modal.vntv-clips-modal,
.vntv-modal.vntv-clipedit-modal {
    background: rgba(0, 0, 0, 0.64);
    color: var(--vntv-fg);
}

.vntv-modal-card.vntv-clips,
.vntv-modal-card.vntv-clipedit {
    border: 1px solid var(--vntv-line);
    border-radius: 12px;
    background: var(--vntv-bg);
    color: var(--vntv-fg);
    box-shadow: var(--elevation-6, 0 20px 32px 0 rgba(0, 0, 0, 0.12));
}

.vntv-clips-modal .vntv-modal-head,
.vntv-clipedit-modal .vntv-modal-head {
    border-bottom-color: var(--vntv-line);
}

.vntv-clips-modal .vntv-modal-icon,
.vntv-clipedit-modal .vntv-modal-icon {
    color: var(--vntv-accent);
}

.vntv-clips-modal .vntv-modal-title,
.vntv-clipedit-modal .vntv-modal-title {
    color: var(--vntv-fg);
}

.vntv-clips-modal .vntv-modal-close,
.vntv-clipedit-modal .vntv-modal-close {
    width: 32px;
    height: 32px;
    border: 0;
    border-radius: 4px;
    color: var(--vntv-icon);
}

.vntv-clips-modal .vntv-modal-close:hover,
.vntv-clipedit-modal .vntv-modal-close:hover {
    background: var(--vntv-fill-hover);
    color: var(--vntv-icon-hover);
}

.vntv-clips-modal .vntv-modal-close:focus-visible,
.vntv-clipedit-modal .vntv-modal-close:focus-visible {
    outline: none;
    box-shadow: 0 0 0 2px var(--vntv-focus);
}

.vntv-clips-body {
    display: flex;
    flex-direction: column;
    gap: 18px;
}

/* A section never gives up its height to the others: the body scrolls. */
.vntv-clips-body > * {
    flex-shrink: 0;
}

/* Just ended and Your drafts: boxed, above the list, because they are the
   reader's own business and short-lived. */
.vntv-clips-ended,
.vntv-clips-drafts {
    padding: 10px 12px;
    border: 1px solid var(--vntv-line);
    border-radius: 4px;
    background: var(--vntv-fill);
}

.vntv-clips-heading-row {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: 2px 6px;
    margin-bottom: 8px;
}

.vntv-clips-heading {
    margin: 0;
    color: var(--vntv-fg);
    font-family: var(--vntv-display);
    font-size: 13px;
    font-weight: 600;
    letter-spacing: 0.06em;
    text-transform: uppercase;
}

.vntv-clips-heading-note {
    color: var(--vntv-second);
    font-size: 11px;
}

.vntv-clips-rows {
    display: flex;
    flex-direction: column;
    gap: 6px;
    margin: 0;
    padding: 0;
    list-style: none;
}

.vntv-clips-tail,
.vntv-clips-draft {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: 6px 12px;
    min-width: 0;
    color: var(--vntv-fg);
    font-size: 12px;
}

/* Once a Just ended row wraps, its button sits under its own sentence: rows
   are kept further apart than that, with a rule between them, so a button
   never reads as the lead-in to the next stream's line. */
.vntv-clips-tail + .vntv-clips-tail {
    padding-top: 8px;
    border-top: 1px solid var(--vntv-line);
}

.vntv-clips-tail-text,
.vntv-clips-draft-text {
    flex: 1 1 220px;
    min-width: 0;
    line-height: 1.4;
}

.vntv-clips-draft-text {
    font-variant-numeric: tabular-nums;
}

.vntv-clips-draft-actions {
    display: inline-flex;
    flex: 0 1 auto;
    flex-wrap: wrap;
    gap: 6px;
    max-width: 100%;
}

/* A button's second-press words wrap on a narrow phone rather than pushing
   the dialog sideways. */
.vntv-clips-draft-actions .vntv-clips-btn {
    flex-shrink: 1;
    max-width: 100%;
    white-space: normal;
    text-align: left;
}

.vntv-clips-draft-busy {
    color: var(--vntv-second);
    font-size: 11px;
}

/* The small buttons in those rows: Clip the end, Edit, Discard.
   Stock's second-rank button, 32px, radius 4, the accent at 0.08 with
   the accent's words, in the page's own face. Discard's first press makes it
   stock's destructive button while it waits for the second; Share's makes it
   the primary one (Clips: Share). */
.vntv-clips-btn {
    display: inline-flex;
    flex: 0 0 auto;
    align-items: center;
    gap: 5px;
    min-height: 32px;
    padding: 0 16px;
    border: 0;
    border-radius: 4px;
    background: rgba(var(--vntv-accent-rgb), 0.08);
    color: var(--vntv-accent);
    cursor: pointer;
    font-family: inherit;
    font-size: 12px;
    font-weight: 600;
    white-space: nowrap;
    transition: background-color 120ms ease, color 120ms ease;
}

.vntv-clips-btn:hover {
    background: rgba(var(--vntv-accent-rgb), 0.12);
}

.vntv-clips-btn:focus-visible {
    outline: none;
    box-shadow: 0 0 0 2px var(--vntv-focus);
}

/* Discard is a destructive action: stock's danger tertiary at rest,
   its filled danger button armed. */
.vntv-clips-btn[data-action="discard"] {
    background: rgba(var(--vntv-danger-rgb), 0.08);
    color: var(--vntv-danger);
}

.vntv-clips-btn[data-action="discard"]:not([data-armed="true"]):hover {
    background: rgba(var(--vntv-danger-rgb), 0.16);
}

.vntv-clips-btn[data-armed="true"] {
    background: var(--vntv-danger-fill);
    color: var(--vntv-on-danger);
}

.vntv-clips-btn[aria-disabled="true"] {
    cursor: default;
    opacity: 0.6;
}

/* Always in the page, so what is put in it is announced; nothing when empty. */
.vntv-clips-status {
    margin: 8px 0 0;
    border: 0;
    padding: 0;
    font-size: 12px;
}

.vntv-clips-status:empty {
    margin: 0;
}

/* The line above the grid: how many, for how long, how many new; then the
   order and the Yours filter. */
.vntv-clips-head {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 8px;
}

.vntv-clips-summary {
    flex: 1 1 auto;
    min-width: 0;
    margin: 0;
    color: var(--vntv-second);
    font-size: 12px;
}

/* The order and the Yours filter, as one item of the line: when the line is
   too narrow for the summary and both, the two go under it together rather
   than Yours dropping on its own. */
.vntv-clips-tools {
    display: flex;
    flex: 0 0 auto;
    align-items: center;
    gap: 8px;
}

/* The order as a stock segmented control (a 1px line, radius 4) and
   Yours as a toggle of the same shape; the chosen one is the accent with the
   accent's words. */
.vntv-clips-sort {
    display: inline-flex;
    flex: 0 0 auto;
    gap: 2px;
    padding: 2px;
    border: 1px solid var(--vntv-line);
    border-radius: 4px;
}

.vntv-clips-pill {
    flex: 0 0 auto;
    min-height: 24px;
    margin: 0;
    padding: 0 10px;
    border: 1px solid transparent;
    border-radius: 4px;
    background: transparent;
    color: var(--vntv-second);
    cursor: pointer;
    font-family: inherit;
    font-size: 12px;
    font-weight: 600;
    transition: background-color 120ms ease, color 120ms ease;
}

.vntv-clips-pill:hover {
    background: var(--vntv-fill-hover);
    color: var(--vntv-fg);
}

.vntv-clips-pill[aria-pressed="true"] {
    background: var(--vntv-accent);
    color: var(--vntv-on-accent);
}

.vntv-clips-pill:focus-visible {
    outline: none;
    box-shadow: 0 0 0 2px var(--vntv-focus);
}

.vntv-clips-filter {
    min-height: 30px;
    border-color: var(--vntv-line);
}

.vntv-clips-filter[aria-pressed="true"] {
    border-color: var(--vntv-accent);
}

/* The grid of clips: as many 200px columns as fit, so two in the popped-out
   panel's window and four on a wide screen. */
.vntv-clips-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(200px, 1fr));
    gap: 16px 14px;
    margin: 0;
    padding: 0;
    list-style: none;
}

/* While the list is on its way: three grey cards, gently lit. */
.vntv-clips-ghost {
    display: flex;
    flex-direction: column;
    gap: 8px;
}

.vntv-clips-ghost-pic,
.vntv-clips-ghost-line {
    display: block;
    border-radius: 4px;
    background: linear-gradient(90deg, var(--vntv-fill) 0, var(--vntv-fill-hover) 40%, var(--vntv-fill) 80%);
    background-size: 200% 100%;
    animation: vntv-clips-shimmer 1.4s ease-in-out infinite;
}

.vntv-clips-ghost-pic {
    aspect-ratio: 16 / 9;
}

.vntv-clips-ghost-line {
    width: 80%;
    height: 10px;
    border-radius: 4px;
}

.vntv-clips-ghost-line + .vntv-clips-ghost-line {
    width: 55%;
}

@keyframes vntv-clips-shimmer {
    from { background-position: 100% 0; }
    to { background-position: -100% 0; }
}

.vntv-clips-empty {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 6px;
    padding: 36px 12px;
    text-align: center;
}

.vntv-clips-empty-icon {
    margin-bottom: 4px;
    color: rgba(var(--vntv-fg-rgb), 0.48);
    line-height: 0;
}

.vntv-clips-empty-title {
    margin: 0;
    color: var(--vntv-fg);
    font-size: 14px;
}

.vntv-clips-empty-hint {
    max-width: 360px;
    margin: 0;
    color: var(--vntv-second);
    font-size: 12px;
    line-height: 1.45;
}

.vntv-clips-empty .vntv-pill {
    margin-top: 6px;
}

/* Focus put here by Retry, for the keyboard's sake only: no ring round the
   whole message, and none on the loading line nobody sees. */
.vntv-clips-empty:focus,
.vntv-clips-loading:focus {
    outline: none;
}

/* ---- Clips: a card in the grid ----
   One button that plays it, holding everything but the upvote, which sits
   over it at the right of the clipper's line: a button cannot hold another.
   A grid of one cell for the two, so the upvote is placed against the
   button's own bottom whatever comes under the card (a refused vote's note,
   which takes the row below). */
.vntv-clipcard {
    position: relative;
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto;
    align-content: start;
    min-width: 0;
}

.vntv-clipcard-play {
    grid-column: 1 / -1;
    grid-row: 1;
    display: flex;
    flex-direction: column;
    gap: 6px;
    width: 100%;
    margin: 0;
    padding: 0 0 2px;
    border: 0;
    border-radius: 4px;
    background: transparent;
    color: var(--vntv-fg);
    cursor: pointer;
    font: inherit;
    text-align: left;
}

/* The keyboard's ring as stock draws it, an outline here, since the
   card's picture would cover a shadow. */
.vntv-clipcard-play:focus-visible {
    outline: 2px solid var(--vntv-focus);
    outline-offset: 3px;
}

.vntv-clipcard-pic {
    position: relative;
    display: block;
    overflow: hidden;
    width: 100%;
    aspect-ratio: 16 / 9;
    border-radius: 4px;
    background: #0b0b0c;
    box-shadow: 0 0 0 1px var(--vntv-line);
    transition: box-shadow 120ms ease;
}

.vntv-clipcard-play:hover .vntv-clipcard-pic {
    box-shadow: 0 0 0 1px var(--vntv-line-focus);
}

.vntv-clipcard-thumb {
    position: absolute;
    inset: 0;
    width: 100%;
    height: 100%;
    object-fit: cover;
    transition: transform 200ms ease;
}

.vntv-clipcard-play:hover .vntv-clipcard-thumb {
    transform: scale(1.03);
}

/* The tags on the picture: its length, when it goes, and NEW.
   The picture's box stays dark (it is where a picture goes); its edge
   is the skin's line, 0.32 pointed at, as a Live now card's is. */
.vntv-clipcard-dur,
.vntv-clipcard-expiry,
.vntv-clipcard-new {
    position: absolute;
    z-index: 1;
    padding: 0 6px;
    border-radius: 4px;
    font-family: var(--vntv-display);
    font-size: 10px;
    font-weight: 600;
    letter-spacing: 0.06em;
    line-height: 17px;
    text-transform: uppercase;
}

.vntv-clipcard-dur {
    right: 6px;
    bottom: 6px;
    background: rgba(0, 0, 0, 0.72);
    color: #ffffff;
    font-variant-numeric: tabular-nums;
    letter-spacing: 0.02em;
}

.vntv-clipcard-expiry {
    bottom: 6px;
    left: 6px;
    background: rgba(0, 0, 0, 0.72);
    color: #f2c94c;
}

/* NEW is a filled tag in the accent, as WATCHING is on a Live now
   card. */
.vntv-clipcard-new {
    top: 6px;
    left: 6px;
    background: var(--vntv-accent);
    color: var(--vntv-on-accent);
}

.vntv-clipcard-title {
    display: block;
    overflow: hidden;
    padding: 0 2px;
    color: var(--vntv-fg);
    font-family: var(--vntv-display);
    font-size: 14px;
    font-weight: 600;
    letter-spacing: 0.02em;
    line-height: 1.3;
    text-overflow: ellipsis;
    white-space: nowrap;
}

.vntv-clipcard-title[data-untitled="true"] {
    color: var(--vntv-second);
}

/* Who made it, with room kept on the right for the upvote; and under it,
   on a line of its own so that neither is cut to make room for the other,
   from whose stream and how long ago. */
.vntv-clipcard-meta {
    display: flex;
    overflow: hidden;
    align-items: center;
    min-width: 0;
    padding: 0 62px 0 2px;
    color: var(--vntv-second);
    font-size: 11px;
    line-height: 20px;
    white-space: nowrap;
}

.vntv-clipcard-where {
    display: block;
    overflow: hidden;
    margin-top: -4px;
    padding: 0 2px;
    color: var(--vntv-second);
    font-size: 11px;
    line-height: 16px;
    text-overflow: ellipsis;
    white-space: nowrap;
}

.vntv-clipcard .vntv-upvote-wrap {
    display: contents;
}

/* Level with the clipper's line: the button's 2px of padding, the 16px line
   under it and the 2px between, then half of what the pill is taller than
   the line. */
.vntv-clipcard .vntv-upvote {
    position: relative;
    z-index: 1;
    grid-column: 2;
    grid-row: 1;
    align-self: end;
    margin-bottom: 19px;
}

/* A refused upvote, said under the card, for a few seconds, and never over
   its picture or its tags. */
.vntv-clipcard .vntv-upvote-note {
    grid-column: 1 / -1;
    grid-row: 2;
    max-width: none;
    margin: 0;
    text-align: left;
}

/* The refusal as the panel's error notice: the theme's error words on
   a faint wash of them. */
.vntv-clipcard .vntv-upvote-note:not(:empty) {
    margin-top: 6px;
    padding: 4px 7px;
    border: 1px solid rgba(var(--vntv-danger-rgb), 0.32);
    border-radius: 4px;
    background: rgba(var(--vntv-danger-rgb), 0.08);
    color: var(--vntv-danger);
}

/* A face and a name, with the Best Clipper's badge when they hold it. */
.vntv-clip-person {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    min-width: 0;
    max-width: 100%;
    vertical-align: middle;
}

.vntv-face.vntv-clip-face {
    width: 16px;
    height: 16px;
    font-size: 7px;
}

.vntv-clip-name {
    overflow: hidden;
    min-width: 0;
    color: var(--vntv-fg);
    text-overflow: ellipsis;
    white-space: nowrap;
}

.vntv-clipper-mark {
    display: inline-flex;
    flex: 0 0 auto;
    align-items: center;
    line-height: 0;
}

/* ---- Clips: the upvote ----
   A thumbs up and "14": pressed, it is red and the hand filled. The count's digits keep
   their width, so voting does not nudge it.
   The shared clip card's thumb in the panel: a 0.24 line, the panel's
   words, pressed the accent with the hand filled, as Mattermost marks a
   reaction of your own. */
.vntv-upvote-wrap {
    display: inline-flex;
    flex: 0 0 auto;
    flex-direction: column;
    align-items: flex-end;
}

.vntv-upvote {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    min-height: 22px;
    margin: 0;
    padding: 0 8px;
    border: 1px solid var(--vntv-line-hover);
    border-radius: 999px;
    background: var(--vntv-bg);
    color: var(--vntv-fg);
    font-family: inherit;
    font-size: 12px;
    font-weight: 600;
    font-variant-numeric: tabular-nums;
    line-height: 1;
    white-space: nowrap;
}

.vntv-upvote svg {
    flex: 0 0 auto;
}

button.vntv-upvote {
    cursor: pointer;
    transition: background-color 120ms ease, border-color 120ms ease, color 120ms ease;
}

button.vntv-upvote:hover {
    border-color: var(--vntv-accent);
    color: var(--vntv-accent);
}

.vntv-upvote[aria-pressed="true"] {
    border-color: var(--vntv-accent);
    background: rgba(var(--vntv-accent-rgb), 0.12);
    color: var(--vntv-accent);
}

.vntv-upvote[aria-pressed="true"] svg {
    fill: currentColor;
}

.vntv-upvote:focus-visible {
    outline: none;
    box-shadow: 0 0 0 2px var(--vntv-focus);
}

.vntv-upvote-static {
    border-style: dashed;
    color: var(--vntv-second);
    cursor: default;
}

.vntv-upvote-note {
    max-width: 260px;
    margin-top: 4px;
    color: var(--vntv-danger);
    font-size: 11px;
    line-height: 1.35;
    text-align: right;
}

.vntv-upvote-note:empty {
    margin-top: 0;
}

/* ---- Clips: the viewer ---- */
.vntv-clipview {
    display: flex;
    flex-direction: column;
    gap: 10px;
    min-width: 0;
}

.vntv-clipview-back {
    display: inline-flex;
    align-self: flex-start;
    align-items: center;
    gap: 4px;
    margin: -4px 0 0 -4px;
    padding: 3px 8px 3px 4px;
    border: 1px solid transparent;
    border-radius: 4px;
    background: transparent;
    color: var(--vntv-second);
    cursor: pointer;
    font-family: inherit;
    font-size: 12px;
    font-weight: 600;
}

/* A stock quiet button: a 0.08 ground pointed at, the stock ring. */
.vntv-clipview-back:hover {
    background: var(--vntv-fill-hover);
    color: var(--vntv-fg);
}

.vntv-clipview-back:focus-visible {
    outline: none;
    box-shadow: 0 0 0 2px var(--vntv-focus);
}

.vntv-clipview-head {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 12px;
}

.vntv-clipview-title {
    flex: 1 1 auto;
    min-width: 0;
    margin: 0;
    color: var(--vntv-fg);
    font-family: var(--vntv-display);
    font-size: 18px;
    font-weight: 600;
    letter-spacing: 0.03em;
    line-height: 1.25;
    overflow-wrap: anywhere;
    text-transform: uppercase;
}

.vntv-clipview-title[data-untitled="true"] {
    color: var(--vntv-second);
    text-transform: none;
}

.vntv-clipview-meta {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 4px;
    margin: 0;
    color: var(--vntv-second);
    font-size: 12px;
    line-height: 1.5;
}

.vntv-clipview-people .vntv-clip-name {
    color: var(--vntv-fg);
}

.vntv-clipview-note {
    margin: 0;
    padding: 6px 10px;
    border-left: 2px solid var(--vntv-line-hover);
    color: var(--vntv-second);
    font-size: 12px;
    line-height: 1.45;
    overflow-wrap: anywhere;
}

.vntv-clipview-actions {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
    padding-top: 4px;
}

/* Their second-press words are long ("Click again to delete — Christopher
   keeps its upvotes"), so they wrap inside the dialog rather than running
   out of it on a phone.
   Stock second-rank buttons, as the editor's are: 32px, radius 4, the
   accent at 0.08 with the accent's words, the page's own face. */
.vntv-clipview-actions .vntv-pill {
    min-height: 32px;
    max-width: 100%;
    padding: 7px 16px;
    border-color: transparent;
    border-radius: 4px;
    color: var(--vntv-accent);
    font-family: inherit;
    font-size: 12px;
    line-height: 16px;
    text-align: left;
    text-transform: none;
    white-space: normal;
}

/* Report stays quiet: a grey second-rank button, the panel's second-rank
   words on its 0.08 fill. */
.vntv-clipview-actions .vntv-pill-muted {
    background: var(--vntv-fill-hover);
    color: var(--vntv-second);
}

.vntv-clipview-actions .vntv-pill-muted:hover {
    background: rgba(var(--vntv-fg-rgb), 0.12);
}

.vntv-clipview-actions .vntv-pill:focus-visible {
    outline: none;
    box-shadow: 0 0 0 2px var(--vntv-focus);
}

/* Delete is a destructive action, so it never looks like Share:
   stock's danger tertiary at rest. */
.vntv-clipview-actions .vntv-pill-danger {
    background: rgba(var(--vntv-danger-rgb), 0.08);
    color: var(--vntv-danger);
}

.vntv-clipview-actions button.vntv-pill-danger:not([data-armed="true"]):hover {
    background: rgba(var(--vntv-danger-rgb), 0.16);
}

/* Waiting for the second press: Delete is stock's filled destructive
   button, Report and Share the primary one (Clips: Share). */
.vntv-clipview-actions .vntv-pill[data-armed="true"] {
    background: var(--vntv-danger-fill);
    color: var(--vntv-on-danger);
}

.vntv-clipview-actions .vntv-pill-muted[data-armed="true"] {
    background: var(--vntv-accent);
    color: var(--vntv-on-accent);
}

.vntv-clipview-actions .vntv-pill[aria-disabled="true"] {
    cursor: default;
    opacity: 0.6;
}

.vntv-clipview-status {
    margin: 0;
    border: 0;
    padding: 0;
    color: var(--vntv-second);
    font-size: 12px;
}

/* The viewer's failure line (Report's and Delete's refusals) is red. Without
   this, .vntv-clipview-status comes later than .vntv-notice-error at the same
   specificity and its grey won, so a refusal read like a neutral note, as
   3.2.1's Download refusals did. In the lighter red, as every word in red on
   the panel's black is. */
.vntv-clipview-status.vntv-notice-error {
    color: var(--vntv-danger);
}

.vntv-clipview-gone {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: 10px;
    margin: 0;
    padding: 24px 2px;
    color: var(--vntv-fg);
    font-size: 13px;
}

.vntv-clipview-gone p {
    margin: 0;
}

/* Where aspect-ratio is missing, the 16:9 boxes keep their shape the old way;
   everything in them is laid over them anyway. */
@supports not (aspect-ratio: 16 / 9) {
    .vntv-clipcard-pic,
    .vntv-clips-ghost-pic {
        height: 0;
        padding-top: 56.25%;
    }
}

/* A phone: the 16px gutter all round, the card as tall as the screen allows,
   and two narrower columns rather than one wide one. */
@media (max-width: 600px) {
    .vntv-modal.vntv-clips-modal {
        padding: 16px;
    }

    .vntv-modal-card.vntv-clips {
        height: 100%;
    }

    .vntv-modal-card.vntv-clips[data-view="locked"] {
        height: auto;
    }

    .vntv-clips-grid {
        grid-template-columns: repeat(auto-fill, minmax(140px, 1fr));
        gap: 14px 10px;
    }
}

/* What a clip's stream was tagged with (addendum 4.14). On a card in the
   grid, under the title, as the Live now card shows what a stream is on:
   small, in the display face, with the kind's icon. */
.vntv-clipcard-tag {
    align-self: flex-start;
    gap: 3px;
    height: 15px;
    margin: -2px 0 0 2px;
    border-width: 0;
    border-radius: 3px;
    padding: 0 5px;
    background: var(--vntv-fill-hover);
    color: var(--vntv-second);
    font-family: var(--vntv-display);
    font-weight: 600;
    font-size: 10px;
    line-height: 15px;
    letter-spacing: 0.05em;
    text-transform: uppercase;
}

/* In the viewer, a line of its own under the title: "Hytale · Game · 2026",
   a button that opens the title's profile when lookups found it. */
.vntv-clipview-tag {
    display: flex;
    min-width: 0;
    margin: -2px 0 0;
}

.vntv-clipview-tag-chip {
    padding: 0 8px;
    font-size: 12px;
    line-height: 20px;
}

/* Not on a picture, on the dialog's ground, so the tag under the
   player's picture is copied: a quiet fill and a line, the words colour, the
   accent when its profile is open, and the stock ring. */
.vntv-onnow.vntv-clipview-tag-chip {
    border-color: var(--vntv-line);
    background: var(--vntv-fill);
    color: var(--vntv-fg);
}

button.vntv-onnow.vntv-clipview-tag-chip {
    border-color: var(--vntv-line-focus);
}

button.vntv-onnow.vntv-clipview-tag-chip:focus-visible {
    box-shadow: 0 0 0 2px var(--vntv-focus);
}

button.vntv-onnow.vntv-clipview-tag-chip[data-open="true"] {
    border-color: var(--vntv-accent);
    background: rgba(var(--vntv-accent-rgb), 0.08);
    color: var(--vntv-accent);
}

@media (hover: hover) {
    button.vntv-onnow.vntv-clipview-tag-chip:hover {
        border-color: var(--vntv-line-focus);
        background: var(--vntv-fill-hover);
    }
}

/* A title's profile opened from inside one of the panel's own dialogs goes
   above it, as the dialog goes above Mattermost's modals. */
.vntv.vntv-mediaprofile[data-layer="modal"] {
    z-index: 1065;
}

/* ---- Clips: the player ----
   A clip in the stream tile's own frame (ClipTile.tsx, Victor's look A,
   design/3.3.0.md): the tile's screen, its line under the picture, and its
   controls over the picture, with a red line of where the clip is along the
   picture's bottom edge. Everything not here is the stream tile's own rule.
   The Clips dialog plays clips in it, and so does the System Console.

   The whole player is never taller than the browser's player was before
   3.3.0, min(56vh, 500px), so the title and the buttons under it stay in
   sight with nothing to scroll: the picture gives up the room of the line
   under it, 38px (the player has no frame). */
.vntv-cliptile {
    --vntv-tile-max: min(56vh - 38px, 462px);
}

/* Full screen keeps room under the picture for the line. */
.vntv-cliptile:fullscreen {
    --vntv-tile-max: calc(100vh - 40px);
}

/* A file that will not play shows no picture at all, as a stream that has
   failed does: the static on black, and the plate. */
.vntv-cliptile[data-state="failed"] video {
    visibility: hidden;
}

/* The row stands clear of the scrubber under it. */
.vntv-cliptile .vntv-hud-bar {
    padding-bottom: 20px;
}

/* Waiting for more of the file while it plays: a ring in the glass of the
   stream tile's switch, in the middle, taking no clicks. */
.vntv-clip-wait {
    position: absolute;
    top: 50%;
    left: 50%;
    z-index: 2;
    display: flex;
    align-items: center;
    justify-content: center;
    width: 56px;
    height: 56px;
    margin: -28px 0 0 -28px;
    border: 2px solid rgba(255, 255, 255, 0.22);
    border-radius: 50%;
    background: rgba(0, 0, 0, 0.6);
    pointer-events: none;
}

/* The ring itself, turning in the plugin red.
   In the accent, on the picture's own dark glass. */
.vntv-clip-ring {
    display: block;
    width: 24px;
    height: 24px;
    border: 3px solid rgba(255, 255, 255, 0.2);
    border-top-color: var(--vntv-accent);
    border-radius: 50%;
    animation: vntv-clip-ring 800ms linear infinite;
}

@keyframes vntv-clip-ring {
    to { transform: rotate(360deg); }
}

/* The time, "0:12 / 0:45", at the end of the line under the picture, after
   whose stream it was: in the display face, in that line's quieter greys.
   That line is on the panel, so the skin's words and second-rank
   words. */
.vntv-clip-time {
    flex: 0 0 auto;
    color: var(--vntv-fg);
    font-family: var(--vntv-display);
    font-size: 13px;
    font-weight: 600;
    font-variant-numeric: tabular-nums;
    letter-spacing: 0.05em;
    line-height: 1;
    white-space: nowrap;
}

.vntv-clip-time-total {
    color: var(--vntv-second);
}

/* Where the clip is: along the picture's bottom edge, a slim red line on a
   dark track, there always, even while the controls above it sleep, so it
   reads over a bright picture. With the controls up it lifts into a scrubber
   with its handle, under their row, on the shade's lighter track. It is one
   of the controls, so it takes clicks only while they are up, but its line
   is drawn regardless. touch-action none, so a drag on a phone scrubs rather
   than scrolling the dialog. */
.vntv-clip-scrub {
    position: absolute;
    right: 0;
    bottom: 0;
    left: 0;
    height: 18px;
    cursor: pointer;
    touch-action: none;
}

.vntv-hud .vntv-clip-scrub {
    opacity: 1;
}

.vntv-clip-scrub:focus {
    outline: none;
}

.vntv-clip-scrub-track {
    position: absolute;
    right: 0;
    bottom: 0;
    left: 0;
    height: 3px;
    background: rgba(0, 0, 0, 0.45);
    transition: height 180ms ease, right 180ms ease, bottom 180ms ease, left 180ms ease, background-color 180ms ease;
}

.vntv-clip-scrub-loaded,
.vntv-clip-scrub-fill {
    position: absolute;
    top: 0;
    bottom: 0;
    left: 0;
    border-radius: inherit;
}

/* What has come of the file, a shade lighter. */
.vntv-clip-scrub-loaded {
    width: var(--vntv-clip-loaded, 0%);
    background: rgba(255, 255, 255, 0.2);
}

/* Where the clip is, in the accent: progress, on the picture's dark
   track. */
.vntv-clip-scrub-fill {
    width: var(--vntv-clip-at, 0%);
    background: var(--vntv-accent);
}

.vntv-clip-scrub-handle {
    position: absolute;
    top: 50%;
    left: var(--vntv-clip-at, 0%);
    width: 12px;
    height: 12px;
    margin: -6px 0 0 -6px;
    border-radius: 50%;
    background: var(--vntv-accent);
    box-shadow: 0 0 0 2px rgba(0, 0, 0, 0.35);
    transform: scale(0);
    transition: transform 160ms ease;
}

/* Held, the handle is bigger and hot. */
.vntv-clip-scrub[data-scrubbing="true"] .vntv-clip-scrub-handle {
    width: 16px;
    height: 16px;
    margin: -8px 0 0 -8px;
    background: var(--vntv-accent);
}

.vntv-clip-scrub:focus-visible .vntv-clip-scrub-handle {
    box-shadow: 0 0 0 2px #ffffff;
}

/* Lifted, with the controls up: pointed at, or held up while paused. */
.vntv-hud[data-awake="true"] .vntv-clip-scrub-track,
.vntv-hud[data-keep-up="true"] .vntv-clip-scrub-track {
    right: 12px;
    bottom: 8px;
    left: 12px;
    height: 4px;
    border-radius: 999px;
    background: rgba(255, 255, 255, 0.2);
}

.vntv-hud[data-awake="true"] .vntv-clip-scrub-handle,
.vntv-hud[data-keep-up="true"] .vntv-clip-scrub-handle {
    transform: scale(1);
}

/* The same from the keyboard, in rules of their own as the reveal is. */
.vntv-hud:has(:focus-visible) .vntv-clip-scrub-track {
    right: 12px;
    bottom: 8px;
    left: 12px;
    height: 4px;
    border-radius: 999px;
    background: rgba(255, 255, 255, 0.2);
}

.vntv-hud:has(:focus-visible) .vntv-clip-scrub-handle {
    transform: scale(1);
}

/* Not over the loading screen, where there is nothing yet to show, and out of
   play there with it: hidden, not only see-through, so it takes no Tab and
   no clicks (ClipTile.tsx). One class above the reveals, which would lift it
   again for a pointer or for focus elsewhere in the row. */
.vntv-cliptile[data-loading="first"] .vntv-hud .vntv-clip-scrub {
    opacity: 0;
    visibility: hidden;
    pointer-events: none;
}

/* A clip has no switch to give the middle to (Hud.tsx), so LOADING and
   CAN'T PLAY stay as they are while the controls are up, instead of taking
   the stream tile's fade (On the picture, above). Rules of their own, like
   the two they undo, for a browser without :has(). */
.vntv-cliptile .vntv-screen:has(> .vntv-hud[data-awake="true"]) .vntv-plate {
    opacity: 1;
}

.vntv-cliptile .vntv-screen:has(> .vntv-hud :focus-visible) .vntv-plate {
    opacity: 1;
}

/* Under the picture, why it will not play and what Download said, on the
   tile's own status line. In the theme's error words, as the panel's
   notices have them. */
.vntv-cliptile .vntv-player-status.vntv-error {
    color: var(--vntv-danger);
}

.vntv-clip-said .vntv-player-status {
    margin: 0;
}

/* In a clip, red is where the clip is: the volume's handle is white, so it
   is never taken for the scrubber's, nor a full volume for a clip that has
   finished. */
.vntv-cliptile .vntv-volume::-webkit-slider-thumb {
    background: #ffffff;
}

.vntv-cliptile .vntv-volume::-moz-range-thumb {
    background: #ffffff;
}

/* ---- Clips: the editor ----
   What a press of the scissors opens (ClipEditor.tsx): the draft's picture,
   the trimmer, the title, and Discard, Later and PUBLISH. The Clips dialog's
   kind of card, wider; on a phone it fills the screen, with its buttons kept
   at the bottom. */
.vntv-modal-card.vntv-clipedit {
    max-width: 920px;
}

/* Focus put on the card itself, by script only: no ring round it. */
.vntv-modal-card.vntv-clipedit:focus {
    outline: none;
}

/* No padding at the foot: the buttons' own bar (below) brings it, so they
   sit the same distance from the card's edge whether or not the rest is
   scrolled under them. */
.vntv-clipedit-body {
    display: flex;
    flex-direction: column;
    gap: 12px;
    padding-bottom: 0;
}

.vntv-clipedit-body > * {
    flex-shrink: 0;
}

/* The picture: 16:9, but never taller than the window leaves room for once
   the head, the trimmer, the title and the buttons are counted (about 430px
   of them), nor than 470px, nor shorter than 160px; past that it is
   letterboxed in black. On a 1366 × 768 laptop's 657px of browser that is
   about 227px, and everything fits without scrolling. */
.vntv-clipedit-stage {
    position: relative;
    overflow: hidden;
    width: 100%;
    max-height: max(160px, min(470px, calc(100vh - 430px)));
    aspect-ratio: 16 / 9;
    border-radius: 4px;
    background: #000000;
}

.vntv-clipedit-stage:focus {
    outline: none;
}

/* The stock ring, outside the picture, on the dialog's ground. */
.vntv-clipedit-stage:focus-visible {
    outline: 2px solid var(--vntv-focus);
    outline-offset: 2px;
}

.vntv-clipedit-stage[inert] {
    pointer-events: none;
}

/* The plate while it cuts, or says why there is no picture, fills it. */
.vntv-clipedit-stage .vntv-screen {
    position: absolute;
    inset: 0;
    width: 100%;
    height: 100%;
    max-width: none;
    max-height: none;
    aspect-ratio: auto;
}

.vntv-clipedit-video {
    display: block;
    width: 100%;
    height: 100%;
    background: #000000;
    cursor: pointer;
    object-fit: contain;
}

/* The big round play in the middle, while it is paused. */
.vntv-clipedit-bigplay {
    position: absolute;
    top: 50%;
    left: 50%;
    display: flex;
    align-items: center;
    justify-content: center;
    width: 64px;
    height: 64px;
    margin: -32px 0 0 -32px;
    padding: 0 0 0 5px;
    border: 0;
    border-radius: 50%;
    background: #ffffff;
    color: #080103;
    cursor: pointer;
    box-shadow: 0 8px 24px rgba(0, 0, 0, 0.55);
    transition: transform 120ms ease;
}

.vntv-clipedit-bigplay svg polygon {
    fill: currentColor;
}

.vntv-clipedit-bigplay:hover {
    transform: scale(1.06);
}

/* On video, the white ring with a dark halo. */
.vntv-clipedit-bigplay:focus-visible {
    outline: none;
    box-shadow: 0 8px 24px rgba(0, 0, 0, 0.55), 0 0 0 3px rgba(0, 0, 0, 0.6), 0 0 0 5px #ffffff;
}

/* Play, where it is in the selection, Loop and the sound, along the bottom
   of the picture on a shade, as the stream's own controls are. */
.vntv-clipedit-bar {
    position: absolute;
    right: 0;
    bottom: 0;
    left: 0;
    display: flex;
    align-items: center;
    gap: 4px;
    padding: 20px 8px 6px;
    background: linear-gradient(to top, rgba(0, 0, 0, 0.85), rgba(0, 0, 0, 0));
}

.vntv-clipedit-control {
    display: flex;
    flex: 0 0 auto;
    align-items: center;
    justify-content: center;
    box-sizing: border-box;
    width: 30px;
    height: 30px;
    padding: 0;
    border: 0;
    border-radius: 5px;
    background: transparent;
    color: #ffffff;
    cursor: pointer;
}

.vntv-clipedit-control svg {
    display: block;
}

.vntv-clipedit-control:hover,
.vntv-clipedit-control:focus-visible {
    background: rgba(255, 255, 255, 0.14);
    outline: none;
}

/* On video, the white ring with a dark halo, as the stream's own
   controls have it. */
.vntv-clipedit-control:focus-visible {
    box-shadow: inset 0 0 0 2px #ffffff, 0 0 0 1px rgba(0, 0, 0, 0.6);
}

/* Loop on is lit; off is plain. Lit in the accent. */
.vntv-clipedit-control[aria-pressed="true"] {
    color: var(--vntv-accent);
}

.vntv-clipedit-control[aria-disabled="true"] {
    cursor: default;
    opacity: 0.4;
}

.vntv-clipedit-time {
    color: #ffffff;
    font-family: var(--vntv-display);
    font-size: 12px;
    font-variant-numeric: tabular-nums;
    letter-spacing: 0.04em;
    white-space: nowrap;
}

.vntv-clipedit-spacer {
    flex: 1 1 auto;
}

/* Publishing: a thin bar running along the foot of the picture, in the
   leaderboard's red, for as long as it takes. In the accent. */
.vntv-clipedit-progress {
    position: absolute;
    right: 0;
    bottom: 0;
    left: 0;
    overflow: hidden;
    height: 3px;
    background: rgba(255, 255, 255, 0.12);
}

.vntv-clipedit-progress-bar {
    position: absolute;
    top: 0;
    bottom: 0;
    left: -35%;
    width: 35%;
    border-radius: 999px;
    background: var(--vntv-accent);
    animation: vntv-clipedit-progress 1.4s ease-in-out infinite;
}

@keyframes vntv-clipedit-progress {
    0% { left: -35%; }
    100% { left: 100%; }
}

/* Where it is from, and what to know about it. */
.vntv-clipedit-context {
    min-height: 17px;
    margin: 0;
    color: var(--vntv-second);
    font-size: 12px;
    line-height: 1.45;
    overflow-wrap: anywhere;
}

.vntv-clipedit-note {
    margin: 0;
    padding: 6px 10px;
    border-left: 2px solid var(--vntv-line-hover);
    color: var(--vntv-second);
    font-size: 12px;
    line-height: 1.45;
}

.vntv-clipedit-failed {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 6px 10px;
    border-left-color: var(--vntv-danger);
}

/* Waiting for the draft, publishing, or locked: there, and dimmed, and
   taking nothing. inert does the taking-nothing where the browser knows it;
   this is for one that does not. */
.vntv-clipedit-trim,
.vntv-clipedit-title {
    transition: opacity 150ms ease;
}

.vntv-clipedit-trim[inert],
.vntv-clipedit-title[inert] {
    opacity: 0.5;
    pointer-events: none;
}

/* The title. */
.vntv-clipedit-title {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 6px 10px;
}

.vntv-clipedit-label {
    color: var(--vntv-fg);
    font-family: var(--vntv-display);
    font-size: 12px;
    font-weight: 600;
    letter-spacing: 0.08em;
    text-transform: uppercase;
}

.vntv-clipedit input.vntv-clipedit-input {
    flex: 1 1 240px;
    box-sizing: border-box;
    min-width: 0;
    height: 34px;
    margin: 0;
    padding: 0 10px;
    border: 1px solid var(--vntv-line);
    border-radius: 4px;
    background: var(--vntv-bg);
    color: var(--vntv-fg);
    font: inherit;
    font-size: 14px;
}

/* Stock's text input: 0.16 at rest, 0.48 pointed at, the accent's
   border and inset line with focus; stock's placeholder at 0.64. */
.vntv-clipedit input.vntv-clipedit-input::placeholder {
    color: rgba(var(--vntv-fg-rgb), 0.64);
}

.vntv-clipedit input.vntv-clipedit-input:hover {
    border-color: rgba(var(--vntv-fg-rgb), 0.48);
}

.vntv-clipedit input.vntv-clipedit-input:focus {
    border-color: var(--vntv-accent);
    outline: none;
    box-shadow: inset 0 0 0 1px var(--vntv-accent);
}

.vntv-clipedit-count {
    color: var(--vntv-second);
    font-size: 11px;
    font-variant-numeric: tabular-nums;
}

/* What went wrong, and what can be done about it. Always in the page, so it
   is announced; taking no room while it says nothing. */
.vntv-clipedit-alert {
    display: flex;
    flex-direction: column;
    gap: 8px;
    padding: 10px 12px;
    border: 1px solid rgba(var(--vntv-danger-rgb), 0.32);
    border-radius: 4px;
    background: rgba(var(--vntv-danger-rgb), 0.08);
}

.vntv-clipedit-alert:empty {
    margin-top: -12px;
    padding: 0;
    border: 0;
}

.vntv-clipedit-alert-text {
    margin: 0;
    color: var(--vntv-danger);
    font-size: 13px;
    line-height: 1.45;
}

.vntv-clipedit-alert-actions {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 8px;
}

/* An action inside a danger alert (Try again, Open drafts) is
   neutral: the page's words on a quiet fill of them. The accent's tint on
   the alert's red tint merged into the box on a red theme. Link your
   stream stays the primary button. */
.vntv-clipedit-alert .vntv-clips-btn {
    background: var(--vntv-fill-hover);
    color: var(--vntv-fg);
}

.vntv-clipedit-alert .vntv-clips-btn:hover {
    background: rgba(var(--vntv-fg-rgb), 0.16);
}

/* What it is doing, read out as it changes. Above the buttons on the page,
   though it comes after them in the page's order, which no reader follows
   for a live line. */
.vntv-clipedit-status {
    order: 1;
    min-height: 17px;
    margin: 0;
    color: var(--vntv-second);
    font-size: 12px;
    line-height: 1.45;
}

/* Saying nothing — an error, which the alert says, or a locked editor — it
   takes no room, as the empty alert does, and still is there to announce. */
.vntv-clipedit-status:empty {
    min-height: 0;
    margin-top: -12px;
}

.vntv-clipedit-actions {
    order: 2;
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 8px;
}

/* Discard, Later and PUBLISH stay at the foot of the card however far the
   rest is scrolled, at every width: a short laptop window, or the notes a
   partial draft brings, can push them past the card's edge. Edge to edge
   over the body's 16px padding, on the card's own black. */
.vntv-clipedit-body > .vntv-clipedit-actions {
    position: sticky;
    bottom: 0;
    z-index: 2;
    margin: 0 -16px;
    padding: 12px 16px 16px;
    border-top: 1px solid var(--vntv-line);
    background: var(--vntv-bg);
}

/* PUBLISH and Done are stock's primary button: the accent, its words
   in the accent's own colour, 32px, radius 4, darkened by 0.08 pointed at;
   not yet possible, stock's disabled look. */
.vntv-clipedit-publish {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    min-width: 116px;
    height: 32px;
    padding: 0 16px;
    border: 0;
    border-radius: 4px;
    background: var(--vntv-accent);
    color: var(--vntv-on-accent);
    cursor: pointer;
    font-family: inherit;
    font-size: 12px;
    font-weight: 600;
}

.vntv-clipedit-publish:hover,
.vntv-clipedit-actions .vntv-clips-btn.vntv-clipedit-primary:hover {
    background: linear-gradient(rgba(0, 0, 0, 0.08), rgba(0, 0, 0, 0.08)), var(--vntv-accent);
}

.vntv-clipedit-publish:focus-visible {
    outline: none;
    box-shadow: 0 0 0 2px var(--vntv-focus);
}

.vntv-clipedit-publish[aria-disabled="true"],
.vntv-clipedit-publish[inert] {
    cursor: default;
    background: var(--vntv-fill-hover);
    color: rgba(var(--vntv-fg-rgb), 0.32);
}

.vntv-clipedit-actions .vntv-clips-btn.vntv-clipedit-primary {
    background: var(--vntv-accent);
    color: var(--vntv-on-accent);
}

/* Published. */
.vntv-clipedit-done {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 10px;
    padding: 36px 8px 28px;
    text-align: center;
}

.vntv-clipedit-done-mark {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    margin: 0;
    color: var(--vntv-fg);
    font-family: var(--vntv-display);
    font-size: 20px;
    font-weight: 600;
    letter-spacing: 0.12em;
    text-transform: uppercase;
}

.vntv-clipedit-done-mark svg {
    color: var(--vntv-accent);
}

.vntv-clipedit-done-text {
    max-width: 460px;
    margin: 0;
    color: var(--vntv-fg);
    font-size: 14px;
    line-height: 1.5;
    overflow-wrap: anywhere;
}

/* In page order: Share's lines go under the row (order 2 is the editor
   body's, which this column is not). */
.vntv-clipedit-actions.vntv-clipedit-done-actions {
    order: 0;
    justify-content: center;
    padding-top: 6px;
}

/* A narrow phone: one button to a line, the width of the column, so Share's
   first press, which lengthens its words, grows it in place rather than
   moving it from under the finger that is about to press it again. */
@media (max-width: 400px) {
    .vntv-clipedit-actions.vntv-clipedit-done-actions {
        align-self: stretch;
    }

    .vntv-clipedit-done-actions > .vntv-clips-btn {
        flex: 1 1 100%;
        justify-content: center;
        text-align: center;
    }
}

/* A phone: the whole screen, the 16px gutter, and the buttons kept at the
   foot of it however far the rest is scrolled. */
@media (max-width: 600px) {
    .vntv-modal.vntv-clipedit-modal {
        padding: 0;
    }

    .vntv-modal-card.vntv-clipedit {
        height: 100%;
        max-height: none;
        border-width: 0;
        border-radius: 0;
    }

    .vntv-clipedit-body {
        padding: 12px 16px 0;
    }

    /* Sticky at every width (above); here also pushed to the foot of the
       full-height card when the rest is short. */
    .vntv-clipedit-body > .vntv-clipedit-actions {
        margin: auto -16px 0;
    }
}

/* ---- Clips: the trimmer ----
   One track the length of the draft: the selection lit between two red
   handles, the rest dimmed, and the playhead a white line. The handles'
   grips are 12px wide; each takes the pointer 44px round, reaching outward,
   so two close together can both be caught.
   The strip is film, so it stays dark on every theme; the selection
   and its handles are the accent, and the keyboard's ring is the stock one
   with the dialog's own ground between. */
.vntv-trim {
    display: flex;
    flex-direction: column;
    gap: 2px;
}

.vntv-trim-track {
    position: relative;
    height: 44px;
    margin: 0 22px;
    cursor: pointer;
    touch-action: none;
    user-select: none;
    -webkit-user-select: none;
}

.vntv-trim-track::before {
    content: '';
    position: absolute;
    top: 10px;
    right: 0;
    bottom: 10px;
    left: 0;
    border-radius: 4px;
    background: repeating-linear-gradient(90deg, #2a2a30 0, #2a2a30 10px, #1d1d22 10px, #1d1d22 12px);
}

.vntv-trim-sel {
    position: absolute;
    top: 8px;
    bottom: 8px;
    box-sizing: border-box;
    border-top: 2px solid var(--vntv-accent);
    border-bottom: 2px solid var(--vntv-accent);
    background: rgba(var(--vntv-accent-rgb), 0.16);
    pointer-events: none;
}

.vntv-trim-dim {
    position: absolute;
    top: 10px;
    bottom: 10px;
    background: rgba(0, 0, 0, 0.6);
    pointer-events: none;
}

.vntv-trim-thumb {
    position: absolute;
    top: 0;
    box-sizing: border-box;
    width: 44px;
    height: 44px;
    cursor: ew-resize;
    outline: none;
}

.vntv-trim-thumb::before {
    content: '';
    position: absolute;
    top: 6px;
    bottom: 6px;
    width: 12px;
    background: var(--vntv-accent);
    box-shadow: 0 1px 4px rgba(0, 0, 0, 0.6);
}

.vntv-trim-thumb::after {
    content: '';
    position: absolute;
    top: 16px;
    bottom: 16px;
    width: 2px;
    border-radius: 1px;
    background: rgba(255, 255, 255, 0.8);
}

.vntv-trim-thumb-start {
    z-index: 2;
    margin-left: -34px;
}

.vntv-trim-thumb-start::before {
    left: 22px;
    border-radius: 4px 0 0 4px;
}

.vntv-trim-thumb-start::after {
    left: 27px;
}

.vntv-trim-thumb-end {
    z-index: 2;
    margin-left: -10px;
}

.vntv-trim-thumb-end::before {
    left: 10px;
    border-radius: 0 4px 4px 0;
}

.vntv-trim-thumb-end::after {
    left: 15px;
}

.vntv-trim-head {
    z-index: 1;
    margin-left: -22px;
    cursor: pointer;
}

.vntv-trim-head::before {
    left: 21px;
    width: 2px;
    top: 4px;
    bottom: 4px;
    border-radius: 1px;
    background: #ffffff;
    box-shadow: 0 0 3px rgba(0, 0, 0, 0.9);
}

.vntv-trim-head::after {
    left: 16px;
    top: 0;
    bottom: auto;
    width: 12px;
    height: 12px;
    border-radius: 50%;
    background: #ffffff;
}

.vntv-trim-thumb:focus-visible::before {
    box-shadow: 0 0 0 2px var(--vntv-bg), 0 0 0 4px var(--vntv-focus);
}

.vntv-trim-head:focus-visible::before {
    box-shadow: none;
}

.vntv-trim-head:focus-visible::after {
    box-shadow: 0 0 0 2px var(--vntv-bg), 0 0 0 4px var(--vntv-focus);
}

.vntv-trim-readout {
    margin: 0;
    color: var(--vntv-second);
    font-family: var(--vntv-display);
    font-size: 12px;
    font-variant-numeric: tabular-nums;
    letter-spacing: 0.06em;
    text-align: center;
}

/* ---- Clips: the streamer's own controls ----
   In the sheet the scissors in the footer's bar open: the switch as a
   settings row, what is kept of their stream under it, then Wipe and their
   clips. Spaced with margins rather than a gap, so the status lines, which
   are always in the page to be announced, take no room while they are
   empty. */
.vntv-clips-streamer {
    display: flex;
    flex-direction: column;
    min-width: 0;
}

/* Lighter than the hint's grey, which falls under 4.5:1 where the footer's
   artwork shows through: this is the notice the streamer has to be able to
   read. The result line under the controls too. */
.vntv-clips-streamer-notice,
.vntv-clips-streamer-said {
    color: var(--vntv-second);
    font-size: 12px;
    line-height: 1.45;
}

.vntv-clips-streamer-notice {
    margin: 4px 0 0;
}

/* 3.2.1: the second switch, downloads, under the first one's notice, set a
   little apart so each notice reads as its own switch's. */
.vntv-clips-streamer-downloads {
    margin-top: 10px;
}

/* And the panel's red, at this size, over that artwork, is under 3.5:1: a
   failure here is said in a lighter red.
   The theme's own error words. */
.vntv-clips-streamer .vntv-notice-error {
    color: var(--vntv-danger);
}

/* Not linked, and kept all the same: said plainly, the whole of the sheet,
   which linking their stream would give them a say over. */
.vntv-clips-attributed {
    margin: 0;
    color: var(--vntv-second);
    font-size: 12px;
    line-height: 1.45;
}

.vntv-clips-streamer-controls {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 8px 12px;
    min-width: 0;
    margin-top: 12px;
}

/* A switch, as a settings row: its words on the left and the track on the
   right, whose thumb slides across, red while it is on. The whole row is the
   button.
   Stock's toggle: a 0.24 track off, the accent on, the thumb in the
   panel's ground off and in the accent's words on; the stock ring. */
.vntv-switch {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    box-sizing: border-box;
    width: 100%;
    min-width: 0;
    margin: 0;
    padding: 4px 0;
    border: none;
    background: transparent;
    color: var(--vntv-fg);
    cursor: pointer;
    font: inherit;
    font-size: 13px;
    text-align: left;
}

.vntv-switch-track {
    position: relative;
    flex: 0 0 auto;
    box-sizing: border-box;
    width: 36px;
    height: 20px;
    border-radius: 999px;
    background: var(--vntv-line-hover);
    transition: background-color 180ms ease;
}

.vntv-switch-thumb {
    position: absolute;
    top: 3px;
    left: 3px;
    width: 14px;
    height: 14px;
    border-radius: 50%;
    background: var(--vntv-bg);
    box-shadow: 0 1px 3px rgba(0, 0, 0, 0.24);
    transition: transform 180ms cubic-bezier(0.22, 1, 0.36, 1), background-color 180ms ease;
}

.vntv-switch[aria-checked="true"] .vntv-switch-track {
    background: var(--vntv-accent);
}

.vntv-switch[aria-checked="true"] .vntv-switch-thumb {
    background: var(--vntv-on-accent);
    transform: translateX(16px);
}

.vntv-switch:hover .vntv-switch-track {
    box-shadow: 0 0 0 1px var(--vntv-line-hover);
}

.vntv-switch:focus-visible {
    outline: none;
}

.vntv-switch:focus-visible .vntv-switch-track {
    box-shadow: 0 0 0 2px var(--vntv-focus);
}

.vntv-switch[aria-disabled="true"] {
    cursor: progress;
}

.vntv-switch-label {
    min-width: 0;
}

/* Wipe: a pill in the panel's own all-caps Oswald, as JOIN on a plan is,
   its words in the lighter red that reads on the sheet's glass. Pressed once it is white on red, asking for the second
   press.
   It destroys what is held, so it is stock's destructive second-rank
   button (the error words on a 0.08 wash of them, 32px, radius 4) and,
   pressed once, the filled destructive button. */
.vntv-clips-streamer-wipe {
    flex: 0 0 auto;
    height: 32px;
    margin: 0;
    padding: 0 16px;
    border: 0;
    border-radius: 4px;
    background: rgba(var(--vntv-danger-rgb), 0.08);
    color: var(--vntv-danger);
    cursor: pointer;
    font-family: inherit;
    font-size: 12px;
    font-weight: 600;
    line-height: 32px;
    white-space: nowrap;
    transition: background-color 120ms ease, color 120ms ease;
}

.vntv-clips-streamer-wipe:hover {
    background: rgba(var(--vntv-danger-rgb), 0.16);
}

.vntv-clips-streamer-wipe:focus-visible {
    outline: none;
    box-shadow: 0 0 0 2px var(--vntv-focus);
}

/* After :hover, so it stays red under the pointer that has just pressed it
   once. */
.vntv-clips-streamer-wipe[data-armed="true"] {
    background: var(--vntv-danger-fill);
    color: var(--vntv-on-danger);
}

.vntv-clips-streamer-wipe[aria-disabled="true"] {
    cursor: default;
    opacity: 0.6;
}

.vntv-clips-streamer-mine {
    color: var(--vntv-second);
    font-size: 12px;
    white-space: nowrap;
}

.vntv-clips-streamer-view {
    margin: 0;
    padding: 0;
    border: none;
    background: transparent;
    color: var(--vntv-link);
    cursor: pointer;
    font: inherit;
    text-decoration: none;
    text-underline-offset: 2px;
}

/* A stock link: the theme's link colour, underlined pointed at. */
.vntv-clips-streamer-view:hover {
    text-decoration: underline;
}

.vntv-clips-streamer-view:focus-visible {
    outline: none;
    border-radius: 2px;
    box-shadow: 0 0 0 2px var(--vntv-focus);
}

.vntv-clips-streamer-said {
    margin: 8px 0 0;
}

.vntv-clips-streamer-said:empty {
    margin: 0;
}

/* ---- The System Console ----
   System Console > Plugins > Matty, where the plugin's own controls sit
   among Mattermost's: a light page, always in Mattermost's Denim theme
   whatever the admin uses, in the console's own Open Sans. Nothing here sets a
   typeface or fights a console rule; colours come from the variables
   Mattermost sets on every page, with Denim's values as the fallbacks.

   One small vocabulary for every card, so they read as one set:
     head    what the card holds, in one line, and "No Save needed" when
             it acts at once rather than with the page's Save button
     quiet   a secondary line, smaller and lighter
     callout a decision or a problem, set apart by a tinted block
     list    rows of picture, title, meta line and pills, ruled between
     sub     a small heading over a list inside a card
     pills   default, primary, pressed, armed (the second click) and disabled,
             each as wide as its widest words so nothing moves when they change
     status  two lines that are always there, for what was just done (or what
             an armed pill's second click will do) and what went wrong, so a
             screen reader hears both
   Mattermost puts each setting's help text under its card, so the cards do not
   explain themselves again. */
.vntv-admin {
    --vntv-admin-text: rgb(var(--center-channel-color-rgb, 63, 67, 80));
    --vntv-admin-quiet: rgba(var(--center-channel-color-rgb, 63, 67, 80), 0.75);
    --vntv-admin-small: rgba(var(--center-channel-color-rgb, 63, 67, 80), 0.8);
    --vntv-admin-rule: rgba(var(--center-channel-color-rgb, 63, 67, 80), 0.12);
    --vntv-admin-wash: rgba(var(--center-channel-color-rgb, 63, 67, 80), 0.06);
    --vntv-admin-edge: rgba(var(--center-channel-color-rgb, 63, 67, 80), 0.24);
    --vntv-admin-accent-rgb: var(--button-bg-rgb, 28, 88, 217);
    --vntv-admin-danger-rgb: var(--error-text-color-rgb, 210, 75, 78);

    /* Words in the danger colour, and white words on it: Denim's error red is
       4.3:1 on white, short of the 4.5 small text needs, so text takes this
       darker one (5.7:1) and the red itself is kept for edges. */
    --vntv-admin-danger-text: rgb(184, 56, 59);
    --vntv-admin-warning-rgb: var(--away-indicator-rgb, 255, 188, 31);

    /* The panel shell, undone: no black fill, no white text, no full-height
       flex column, no clipping. */
    display: block;
    height: auto;
    overflow: visible;
    background: transparent;
    color: var(--vntv-admin-text);
    font-size: 14px;
    line-height: 20px;
}

/* The head: the card's state in one line, with the tag beside it. As tall as
   the console's own inputs, so its words sit level with the setting's label
   as an input's would. */
.vntv-admin-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    min-height: 34px;
    margin: 0 0 4px;
}

.vntv-admin-head > .vntv-pill {
    flex: 0 0 auto;
    white-space: nowrap;
}

.vntv-admin-stat {
    flex: 1 1 auto;
    min-width: 0;
    margin: 0;
}

.vntv-admin-tag {
    flex: 0 0 auto;
    padding: 2px 8px;
    border-radius: 10px;
    background: var(--vntv-admin-wash);
    color: var(--vntv-admin-small);
    font-size: 12px;
    font-weight: 600;
    line-height: 16px;
    white-space: nowrap;
}

.vntv-admin-line {
    margin: 0 0 4px;
    font-size: 14px;
    line-height: 20px;
}

/* After .vntv-admin-line, which it is usually worn with. A colour rather than
   an opacity, so a link or a code span inside it stays readable. */
.vntv-admin-quiet {
    color: var(--vntv-admin-quiet);
    font-size: 13px;
    line-height: 18px;
}

.vntv-admin .vntv-notice-error {
    padding: 0;
    border: none;
    color: var(--vntv-admin-danger-text);
}

.vntv-admin-code {
    font-size: 12px;
    word-break: break-all;
}

/* Something that wants a decision, or has gone wrong: a tinted block with an
   edge, the words in the page's own colour so they stay easy to read. */
.vntv-admin-callout {
    margin: 8px 0;
    padding: 10px 12px;
    border-left: 3px solid rgb(var(--vntv-admin-accent-rgb));
    border-radius: 4px;
    background: rgba(var(--vntv-admin-accent-rgb), 0.06);
    font-size: 13px;
    line-height: 18px;
}

.vntv-admin-callout[data-tone="warning"] {
    border-left-color: rgb(var(--vntv-admin-warning-rgb));
    background: rgba(var(--vntv-admin-warning-rgb), 0.14);
}

.vntv-admin-callout[data-tone="error"] {
    border-left-color: rgb(var(--vntv-admin-danger-rgb));
    background: rgba(var(--vntv-admin-danger-rgb), 0.06);
}

.vntv-admin-callout > .vntv-admin-line {
    font-size: inherit;
    line-height: inherit;
}

.vntv-admin-callout > .vntv-admin-line:last-child {
    margin-bottom: 0;
}

/* The two lines that are always there. Empty, they take no room but stay in
   the page, so what is put in them is announced. */
.vntv-admin-status {
    margin: 10px 0 0;
    font-size: 13px;
    line-height: 18px;
}

.vntv-admin-status[data-tone="error"] {
    color: var(--vntv-admin-danger-text);
}

.vntv-admin-status:empty {
    margin: 0;
}

/* Where focus is put when the pill that had it goes away: a ring for whoever
   came by keyboard, none for a mouse. */
.vntv-admin-status:focus-visible {
    border-radius: 2px;
    outline: 2px solid rgba(var(--vntv-admin-accent-rgb), 0.5);
    outline-offset: 2px;
}

/* A line above a card's pills, read before the press. */
.vntv-admin-lead {
    margin: 10px 0 0;
}

/* A small heading over a list inside a card. Higher than the console's own
   .admin-console h4, which would otherwise make it 18px with 2em under it. */
.vntv-admin .vntv-admin-sub {
    margin: 20px 0 6px;
    padding: 0;
    color: var(--vntv-admin-quiet);
    font-family: inherit;
    font-size: 12px;
    font-weight: 600;
    letter-spacing: 0.04em;
    line-height: 16px;
    text-transform: uppercase;
}

/* The stream key beside a 24/7 channel's name, as typed. */
.vntv-admin-subkey {
    margin-left: 6px;
    font-weight: 400;
    text-transform: none;
}

/* A list of rows, ruled above, below and between. Higher than the console's
   ul, which indents every list 22px. */
.vntv-admin .vntv-admin-list {
    margin: 4px 0 0;
    padding: 0;
    border-top: 1px solid var(--vntv-admin-rule);
    list-style: none;
}

.vntv-admin-row {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 6px 12px;
    padding: 10px 0;
    border-bottom: 1px solid var(--vntv-admin-rule);
}

.vntv-admin-row[data-align="top"] {
    align-items: flex-start;
}

.vntv-admin-row-main {
    flex: 1 1 150px;
    min-width: 0;
}

.vntv-admin-row-title {
    display: flex;
    align-items: center;
    gap: 8px;
    min-width: 0;
    margin: 0;
    font-weight: 600;
}

.vntv-admin-row-name {
    overflow: hidden;
    min-width: 0;
    text-overflow: ellipsis;
    white-space: nowrap;
}

.vntv-admin-row-meta {
    margin: 2px 0 0;
    color: var(--vntv-admin-quiet);
    font-size: 13px;
    line-height: 18px;
}

.vntv-admin-row-text {
    flex: 1 1 240px;
    min-width: 0;
    font-size: 13px;
    line-height: 18px;
}

.vntv-admin-row[data-state="failed"] .vntv-admin-row-text,
.vntv-admin-row[data-state="full"] .vntv-admin-row-text {
    color: var(--vntv-admin-danger-text);
}

/* A reported clip: a red edge inside its row. */
.vntv-admin-row[data-reported="true"] {
    padding-left: 12px;
    box-shadow: inset 3px 0 0 rgb(var(--vntv-admin-danger-rgb));
}

.vntv-admin-row-main > .vntv-admin-actions {
    margin-top: 8px;
}

/* What the last action did, again beside the row it was on. */
.vntv-admin-row-result {
    flex: 1 0 100%;
    margin: 0;
    font-size: 13px;
    line-height: 18px;
}

/* One preview scale for every picture: 56px high, square, round or 16:9. */
.vntv-admin-thumb {
    display: flex;
    flex: 0 0 auto;
    align-items: center;
    justify-content: center;
    overflow: hidden;
    width: 56px;
    height: 56px;
    border-radius: 6px;
    background: var(--vntv-admin-wash);
    color: var(--vntv-admin-quiet);
}

.vntv-admin-thumb[data-shape="circle"] {
    border-radius: 50%;
}

.vntv-admin-thumb[data-shape="wide"] {
    width: 100px;
}

/* The emblem, the ghost and a clip are seen on black, so that is what their
   preview shows. */
.vntv-admin-thumb[data-dark="true"] {
    background: #000000;
    color: rgba(255, 255, 255, 0.4);
}

.vntv-admin-thumb[data-pad="small"] {
    padding: 6px;
}

.vntv-admin-thumb[data-pad="large"] {
    padding: 14px;
}

.vntv-admin-thumb-image {
    width: 100%;
    height: 100%;
    object-fit: cover;
}

.vntv-admin-thumb[data-fit="contain"] .vntv-admin-thumb-image {
    object-fit: contain;
}

.vntv-admin-thumb-none {
    font-size: 18px;
    font-weight: 600;
    line-height: 1;
}

/* As wide as a card picture, so the words beside a round picture and a wide
   one start in one column; the round one at its left, where it stays when a
   narrow page puts the words under it. */
.vntv-admin-thumbslot {
    display: flex;
    flex: 0 0 100px;
    justify-content: flex-start;
}

/* A word on a row: Custom, Reported. */
.vntv-admin-flag {
    flex: 0 0 auto;
    padding: 0 6px;
    border-radius: 4px;
    background: var(--vntv-admin-wash);
    color: var(--vntv-admin-small);
    font-size: 11px;
    font-weight: 600;
    letter-spacing: 0.04em;
    line-height: 16px;
    text-transform: uppercase;
}

.vntv-admin-flag[data-tone="accent"] {
    background: rgba(var(--vntv-admin-accent-rgb), 0.1);
    color: rgb(var(--vntv-admin-accent-rgb));
}

.vntv-admin-flag[data-tone="danger"] {
    background: var(--vntv-admin-danger-text);
    color: #ffffff;
}

/* ---- The System Console: the app check ----
   This plugin's release in a line over the verdicts, its manifest's SHA-256
   on a line of its own; a verdict's word in the console's colours, and the
   server's reason in the page's own words under it. */
.vntv-admin .vntv-admin-sub.vntv-appcheck-first {
    margin-top: 8px;
}

.vntv-admin .vntv-appcheck-sum {
    display: block;
    margin-top: 2px;
    color: var(--vntv-admin-text);

    /* Broken only after a colon, where the markup allows it. */
    word-break: normal;
}

.vntv-admin-row-meta.vntv-appcheck-reason {
    color: var(--vntv-admin-text);
}

/* Its words are the danger red taken part of the way to the console's own
   words: darker on a light console, lighter on a dark one, so they read on
   the tint either way. */
.vntv-admin-flag[data-state="unverified"] {
    background: rgba(var(--vntv-admin-danger-rgb), 0.12);
    color: color-mix(in srgb, var(--vntv-admin-danger-text) 60%, var(--vntv-admin-text));
}

/* Verified, likewise: the accent taken part of the way to the console's
   words. */
.vntv-admin-flag[data-state="verified"] {
    color: color-mix(in srgb, rgb(var(--vntv-admin-accent-rgb)) 70%, var(--vntv-admin-text));
}

.vntv-admin-flag[data-state="stale"] {
    background: rgba(var(--vntv-admin-warning-rgb), 0.24);
    color: var(--vntv-admin-text);
}

/* ---- The System Console: push notifications ----
   A push server's word in the app check's colours: Refused as Not verified,
   Failing as No longer accepted. What the words mean, under the rows, a
   little apart from them. */
.vntv-admin-flag[data-state="refused"] {
    background: rgba(var(--vntv-admin-danger-rgb), 0.12);
    color: color-mix(in srgb, var(--vntv-admin-danger-text) 60%, var(--vntv-admin-text));
}

.vntv-admin-flag[data-state="failing"] {
    background: rgba(var(--vntv-admin-warning-rgb), 0.24);
    color: var(--vntv-admin-text);
}

.vntv-admin .vntv-push-why {
    margin-top: 8px;
}

/* The artwork: "None" in a preview's place, a word rather than initials. */
.vntv-admin-thumb-none.vntv-brand-none {
    font-size: 12px;
}

/* ---- The System Console: pills ----
   The panel's red Oswald capsules, redrawn for a white page in the console's
   own type. One shape; the state says what it is. */
.vntv-admin-actions {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 8px;
    margin-top: 12px;
}

.vntv-admin .vntv-pill {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: 6px;
    max-width: 100%;
    min-height: 28px;
    padding: 3px 12px;
    border: 1px solid var(--vntv-admin-edge);
    border-radius: 14px;
    background: rgb(var(--center-channel-bg-rgb, 255, 255, 255));
    color: var(--vntv-admin-text);
    font-family: inherit;
    font-size: 13px;
    font-weight: 600;
    line-height: 20px;
    text-align: center;
    text-transform: none;
    white-space: normal;
    transition: background-color 120ms ease, border-color 120ms ease, color 120ms ease;
}

.vntv-admin button.vntv-pill:hover:enabled:not([aria-disabled="true"]) {
    border-color: rgba(var(--center-channel-color-rgb, 63, 67, 80), 0.4);
    background: var(--vntv-admin-wash);
}

/* The console takes every outline away (*:focus), so the ring is drawn. */
.vntv-admin button.vntv-pill:focus-visible {
    outline: none;
    box-shadow: 0 0 0 2px rgb(var(--center-channel-bg-rgb, 255, 255, 255)), 0 0 0 4px rgba(var(--vntv-admin-accent-rgb), 0.5);
}

/* The card's main action, in Mattermost's own tertiary style (a tint of the
   button colour, the words in it) rather than filled: the page's one filled
   button is Save. */
.vntv-admin .vntv-pill[data-tone="primary"] {
    border-color: transparent;
    background: rgba(var(--vntv-admin-accent-rgb), 0.08);
    color: rgb(var(--vntv-admin-accent-rgb));
}

.vntv-admin button.vntv-pill[data-tone="primary"]:hover:enabled:not([aria-disabled="true"]) {
    border-color: transparent;
    background: rgba(var(--vntv-admin-accent-rgb), 0.14);
}

/* A pill's words at rest, armed and working, in one grid cell, so it keeps
   the width of the longest: the second click lands where the first did, and
   nothing beside it moves. Hidden faces are visibility: hidden, which also
   keeps them out of the pill's accessible name. */
.vntv-admin .vntv-pill-faces {
    display: grid;
    min-width: 0;
}

.vntv-admin .vntv-pill-face {
    grid-area: 1 / 1;
    min-width: 0;
}

.vntv-admin .vntv-pill-face:not([data-shown="true"]) {
    visibility: hidden;
}

.vntv-admin .vntv-pill[aria-pressed="true"] {
    border-color: rgb(var(--vntv-admin-accent-rgb));
    background: rgba(var(--vntv-admin-accent-rgb), 0.08);
    color: rgb(var(--vntv-admin-accent-rgb));
}

/* Waiting for its second click: the one thing on the card in the danger
   colour, so there is no doubt which button will act. */
.vntv-admin .vntv-pill[data-armed="true"] {
    border-color: var(--vntv-admin-danger-text);
    background: var(--vntv-admin-danger-text);
    color: #ffffff;
}

.vntv-admin button.vntv-pill[data-armed="true"]:hover:enabled:not([aria-disabled="true"]) {
    border-color: rgb(160, 48, 51);
    background: rgb(160, 48, 51);
}

/* Disabled looks like itself, faded, on the console's own ground: the panel's
   own disabled pill is drawn for black and would turn these grey-on-grey.
   aria-disabled is Wipe's, which stays focusable while it works. */
.vntv-admin button.vntv-pill:disabled,
.vntv-admin button.vntv-pill[aria-disabled="true"] {
    border-color: var(--vntv-admin-edge);
    background: rgb(var(--center-channel-bg-rgb, 255, 255, 255));
    color: var(--vntv-admin-text);
    cursor: default;
    opacity: 0.48;
}

/* Driven by a pill rather than shown as itself: a bare file input next to
   pills looks like a different kind of control from the one it is. Hidden with
   clip rather than display:none so the click still lands. */
.vntv-admin-file {
    position: absolute;
    width: 1px;
    height: 1px;
    overflow: hidden;
    clip-path: inset(50%);
    opacity: 0;
    pointer-events: none;
}

/* A checkbox that reads as a sentence: the label is the whole row. Higher than
   Bootstrap's label, which would make the sentence bold. */
.vntv-admin .vntv-admin-check {
    display: flex;
    align-items: flex-start;
    gap: 8px;
    margin: 0;
    font-size: 14px;
    font-weight: 400;
    line-height: 20px;
    cursor: pointer;
}

.vntv-admin-check input {
    flex: 0 0 auto;
    margin: 3px 0 0;
    accent-color: rgb(var(--vntv-admin-accent-rgb));
    cursor: pointer;
}

.vntv-admin-checknote {
    margin: 6px 0 0 24px;
}

.vntv-admin-check input:focus-visible {
    outline: 2px solid rgba(var(--vntv-admin-accent-rgb), 0.5);
    outline-offset: 2px;
}

/* The console's own inputs: 34px, a hairline border, the blue glow on focus. */
.vntv-admin .vntv-input,
.vntv-admin .vntv-select {
    height: 34px;
    padding: 6px 10px;
    border: 1px solid rgba(0, 0, 0, 0.15);
    border-radius: 2px;
    background: #ffffff;
    box-shadow: inset 0 1px 1px rgba(0, 0, 0, 0.075);
    color: var(--vntv-admin-text);
    font: inherit;
    font-size: 14px;
    line-height: 20px;
}

.vntv-admin .vntv-input:focus,
.vntv-admin .vntv-select:focus {
    border-color: #66afe9;
    box-shadow: inset 0 1px 1px rgba(0, 0, 0, 0.075), 0 0 8px rgba(102, 175, 233, 0.75);
    outline: none;
}

.vntv-admin .vntv-input:disabled,
.vntv-admin .vntv-select:disabled {
    background: rgb(245, 245, 245);
    cursor: not-allowed;
}

.vntv-admin .vntv-select {
    padding-right: 6px;
    background-image: none;
    appearance: auto;
}

/* ---- The System Console: the cropper ----
   Choosing the part of a photo to use. The square itself is black, because
   that is what the picture will be seen against; the ring's shadow darkens
   everything outside the circle, so what is left bright is what everybody
   will see. */
.vntv-crop {
    display: flex;
    flex-wrap: wrap;
    align-items: flex-start;
    gap: 16px;
    margin: 10px 0 0;
    padding: 12px;
    border: 1px solid var(--vntv-admin-rule);
    border-radius: 6px;
    background: #ffffff;
}

.vntv-crop-stage {
    position: relative;
    flex: 0 0 auto;
    width: 240px;
    height: 240px;
    overflow: hidden;
    border-radius: 6px;
    background: #000000;
    cursor: grab;
    touch-action: none;
    user-select: none;
}

.vntv-crop-stage[data-shape="wide"] {
    width: 320px;
    height: 180px;
}

.vntv-crop-stage[data-dragging="true"] {
    cursor: grabbing;
}

.vntv-crop-stage:focus-visible {
    outline: 2px solid rgba(var(--vntv-admin-accent-rgb), 0.6);
    outline-offset: 2px;
}

/* max-width undone: the console's own stylesheet caps images at their
   container, which would squash a zoomed photo back into the square. */
.vntv-crop-image {
    position: absolute;
    top: 0;
    left: 0;
    max-width: none;
    max-height: none;
    pointer-events: none;
}

.vntv-crop-ring {
    position: absolute;
    inset: 0;
    border: 2px solid rgba(255, 255, 255, 0.85);
    border-radius: 50%;
    box-shadow: 0 0 0 999px rgba(0, 0, 0, 0.55);
    pointer-events: none;
}

.vntv-crop-stage[data-shape="wide"] .vntv-crop-ring {
    border-radius: 6px;
}

.vntv-crop-side {
    flex: 1 1 180px;
    min-width: 0;
}

.vntv-admin .vntv-crop-zoom {
    display: flex;
    align-items: center;
    gap: 8px;
    margin: 0 0 6px;
    font-weight: 600;
}

.vntv-crop-zoom input {
    flex: 1 1 auto;
    min-width: 0;
    accent-color: rgb(var(--vntv-admin-accent-rgb));
}

/* The console takes every outline away (*:focus), so the ring is drawn. */
.vntv-crop-zoom input:focus-visible {
    border-radius: 2px;
    outline: 2px solid rgba(var(--vntv-admin-accent-rgb), 0.5);
    outline-offset: 2px;
}

/* ---- The System Console: the cards ---- */

/* The bot's picture, round as Mattermost shows it beside a post. */
.vntv-avatar {
    display: flex;
    align-items: center;
    gap: 12px;
    margin: 6px 0 0;
}

.vntv-avatar-side {
    flex: 1 1 auto;
    min-width: 0;
}

/* The theme's colours, a handful of chips. A border on each, because half
   this theme is black on a white page and a chip with no edge would look like
   a hole. */
.vntv-theme-swatches {
    display: flex;
    flex-wrap: wrap;
    gap: 6px 14px;
    margin: 4px 0 0;
}

.vntv-theme-swatch {
    display: flex;
    align-items: center;
    gap: 6px;
}

.vntv-theme-chip {
    width: 18px;
    height: 18px;
    border: 1px solid var(--vntv-admin-edge);
    border-radius: 4px;
}

.vntv-theme-name {
    color: var(--vntv-admin-quiet);
    font-size: 13px;
    line-height: 18px;
}

/* One card per person rather than a row of unlabelled boxes, each field
   labelled above itself. */
.vntv-bd-card {
    margin: 0 0 8px;
    padding: 12px;
    border: 1px solid var(--vntv-admin-rule);
    border-radius: 6px;
    background: #ffffff;
}

.vntv-bd-line {
    display: flex;
    flex-wrap: wrap;
    align-items: flex-end;
    gap: 10px;
    margin-bottom: 10px;
}

/* A label element: Bootstrap would make it bold, with 5px under it. */
.vntv-admin .vntv-bd-field {
    display: flex;
    flex-direction: column;
    gap: 4px;
    margin: 0;
    font-weight: 400;
}

.vntv-bd-grow {
    flex: 1 1 180px;
    min-width: 0;
}

.vntv-bd-label {
    color: var(--vntv-admin-quiet);
    font-size: 12px;
    font-weight: 600;
    line-height: 16px;
}

.vntv-bd-date,
.vntv-bd-who {
    display: flex;
    gap: 6px;
}

.vntv-bd-day {
    flex: 0 0 68px;
}

.vntv-bd-year {
    flex: 0 0 84px;
}

.vntv-bd-foot {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 10px;
    padding-top: 10px;
    border-top: 1px solid var(--vntv-admin-rule);
}

/* Who is next, and what they are turning. */
.vntv-bd-when {
    font-size: 13px;
    line-height: 18px;
}

.vntv-bd-when:not(.vntv-notice-error) {
    color: var(--vntv-admin-quiet);
}

.vntv-bd-list {
    margin-top: 10px;
}

.vntv-bd-raw {
    width: 100%;
    min-height: 180px;
    margin-top: 6px;
    padding: 8px 10px;
    border: 1px solid rgba(0, 0, 0, 0.15);
    border-radius: 2px;
    font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
    font-size: 12px;
}

/* The console's own input glow, as .vntv-input has. */
.vntv-admin .vntv-bd-raw:focus {
    border-color: #66afe9;
    box-shadow: inset 0 1px 1px rgba(0, 0, 0, 0.075), 0 0 8px rgba(102, 175, 233, 0.75);
    outline: none;
}

/* A clip playing in its row: under the row, letterboxed on black. */
.vntv-admin-clips-player {
    flex: 1 0 100%;
    max-width: 560px;
}

.vntv-admin-clips-player .vntv-clipview-note {
    color: inherit;
}

/* Upvotes in a console line: a thumbs up and the number, kept together. */
.vntv-admin-votes {
    white-space: nowrap;
}

.vntv-admin-votes svg {
    margin-right: 3px;
    vertical-align: -2px;
}

/* ---- The System Console: Access ----
   Who is let in to watch without a token, and unlinking somebody's stream
   (AdminAccess.tsx): the shared card pieces above, and these few of its own. */

/* A username box with its pill beside it, as wide as a username needs. */
.vntv-access-entry {
    display: flex;
    align-items: center;
    gap: 8px;
    max-width: 420px;
    margin: 8px 0 4px;
}

.vntv-access-entry .vntv-input {
    flex: 1 1 auto;
    min-width: 0;
}

/* Both pills as wide as the wider of them (Unlink, which also holds
   "Unlinking…"), with a little to spare for another font, so the two
   username boxes end at the same place. */
.vntv-access-entry .vntv-pill {
    flex: 0 0 auto;
    min-width: 8em;
}

/* A person's face beside their name: smaller than a picture that is the
   row's subject, as the 24/7 pictures and the clips are. */
.vntv-admin-thumb[data-size="small"] {
    width: 32px;
    height: 32px;
}

/* The username beside a display name, and "(you)": the row title's weight
   undone, so the name reads first. Never shortened: the username is what an
   admin acts on and types into Unlink, and is 22 characters at most, so on a
   narrow page only the display name before it gives way. */
.vntv-access-handle {
    flex: 0 0 auto;
    color: var(--vntv-admin-quiet);
    font-weight: 400;
    white-space: nowrap;
}

/* Taken off a moment ago: still there, greyed, with Undo at full strength
   beside it. */
.vntv-admin-row[data-gone="true"] .vntv-admin-thumb,
.vntv-admin-row[data-gone="true"] .vntv-admin-row-main {
    opacity: 0.55;
}

/* ---- Clips: Share ----
   The Share button takes the look of the row it is put in (a pill in the
   viewer, a button in the editor), and only lets its longer words wrap:
   "Click again to post it in Arma night" on a phone. Its lines go under the
   row, in one box: where the clip went, or why it did not, with Try again. */
.vntv-share {
    max-width: 100%;
    white-space: normal;
    text-align: left;
}

.vntv-share-status {
    display: flex;
    flex-direction: column;
    color: var(--vntv-second);
    font-size: 12px;
    line-height: 1.45;
}

.vntv-share-status p {
    margin: 0;
}

.vntv-share-status .vntv-notice-error {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 6px 10px;
}

/* Its Try again. The stock second-rank button in the accent, beside
   the error words. */
.vntv-share-status .vntv-pill {
    border-color: transparent;
    border-radius: 4px;
    font-family: inherit;
    font-size: 12px;
    text-transform: none;
}

/* Share's first press asks for a second: stock's primary button meanwhile,
   in the viewer's row and in the editor's alike. */
.vntv-share[data-armed="true"],
.vntv-clipview-actions .vntv-pill.vntv-share[data-armed="true"] {
    background: var(--vntv-accent);
    color: var(--vntv-on-accent);
}

.vntv-clipedit-done .vntv-share-status {
    align-items: center;
}

/* Centred like the rest of the column, also when Try again goes onto a line
   of its own on a phone. */
.vntv-clipedit-done .vntv-share-status .vntv-notice-error {
    justify-content: center;
}

/* ---- Clips: a shared clip in a channel ----
   The card a shared clip's post is drawn as (ClipPostCard.tsx). It sits on
   Mattermost's channel, not in the panel, so it wears Mattermost's theme:
   every colour is one of the variables Mattermost sets for every theme, which
   reads on Denim's white and on Onyx's black alike, and the accent is
   only on the eyebrow's clapper. The .vntv root brings the box-sizing, the
   variables and Oswald, and this first rule undoes the rest of it: no black,
   no white text, no full-height column.

   A 16:9 picture and the words beside it, about 99px tall so a permalink
   preview, which never cuts a plugin's post short, stays small; under about
   340px the words go under the picture. The picture is black in every theme,
   which is what a video is. */
.vntv.vntv-postclip {
    flex-direction: row;
    flex-wrap: wrap;
    align-items: stretch;
    gap: 8px 12px;
    width: 100%;
    max-width: 480px;
    height: auto;
    margin: 2px 0;
    padding: 8px;
    border: 1px solid rgba(var(--center-channel-color-rgb, 63, 67, 80), 0.16);
    border-radius: 8px;
    overflow: hidden;
    background: rgba(var(--center-channel-color-rgb, 63, 67, 80), 0.04);
    color: var(--center-channel-color, #3f4350);
    font-size: 13px;
    line-height: 1.35;
    text-align: left;
}

/* Inside a permalink preview Mattermost follows the permalink on a click
   whose target is not a button, a link, a picture or an i, an svg included:
   nothing inside a button, and no svg, takes the pointer, so a click lands
   on the button itself or on plain words. */
.vntv-postclip button *,
.vntv-postclip svg {
    pointer-events: none;
}

.vntv-postclip-pic {
    position: relative;
    display: block;
    flex: 0 0 auto;
    width: 144px;
    height: 81px;
    margin: 0;
    border: 0;
    border-radius: 6px;
    padding: 0;
    overflow: hidden;
    background: #000000;
    cursor: pointer;
}

/* Compact: the smaller picture beside the words' middle, not stuck to the
   top of them with a strip of nothing under it, and the words a little
   closer together. */
.vntv-postclip[data-compact="true"] .vntv-postclip-pic {
    align-self: center;
    width: 112px;
    height: 63px;
}

.vntv-postclip[data-compact="true"] .vntv-postclip-body {
    gap: 1px;
}

.vntv-postclip[data-dimmed="true"] .vntv-postclip-pic {
    cursor: default;
}

.vntv-postclip[data-dimmed="true"] .vntv-postclip-placeholder {
    opacity: 0.5;
}

.vntv-postclip-thumb {
    position: absolute;
    inset: 0;
    display: block;
    width: 100%;
    height: 100%;
    object-fit: cover;
}

.vntv-postclip-placeholder {
    position: absolute;
    inset: 0;
    display: flex;
    align-items: center;
    justify-content: center;
    color: rgba(255, 255, 255, 0.4);
}

/* Asking the server about it: a slow breath, stilled under less motion. */
.vntv-postclip[data-state="loading"] .vntv-postclip-placeholder {
    animation: vntv-postclip-pulse 1.6s ease-in-out infinite;
}

@keyframes vntv-postclip-pulse {
    0%, 100% { opacity: 1; }
    50% { opacity: 0.35; }
}

.vntv-postclip-dur {
    position: absolute;
    right: 4px;
    bottom: 4px;
    padding: 0 4px;
    border-radius: 3px;
    background: rgba(0, 0, 0, 0.72);
    color: #ffffff;
    font-size: 11px;
    font-weight: 600;
    line-height: 16px;
    font-variant-numeric: tabular-nums;
}

/* At least 170px beside the picture, or the words go under it: so a card
   under about 344px wide (a thread in the side panel) stacks, and its words
   get the whole width, rather than squeezing the streamer and the length
   off the meta line. */
.vntv-postclip-body {
    display: flex;
    flex: 1 1 170px;
    flex-direction: column;
    justify-content: center;
    gap: 3px;
    min-width: 0;
}

.vntv-postclip-eyebrow {
    display: flex;
    align-items: center;
    gap: 5px;
    margin: 0;
    color: rgba(var(--center-channel-color-rgb, 63, 67, 80), 0.75);
    font-family: var(--vntv-display);
    font-size: 10px;
    font-weight: 600;
    letter-spacing: 0.1em;
    line-height: 13px;
    text-transform: uppercase;
}

/* The eyebrow's clapper in the theme's accent. */
.vntv-postclip-icon {
    display: inline-flex;
    flex: 0 0 auto;
    color: var(--vntv-accent);
    font-style: normal;
}

.vntv-postclip-title {
    display: flex;
    align-items: center;
    gap: 6px;
    min-width: 0;
    margin: 0;
    font-size: 14px;
    font-weight: 600;
    line-height: 18px;
}

.vntv-postclip-name {
    flex: 0 1 auto;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
}

/* What the stream was tagged with, after the title: the kind's icon and the
   title, as the Live now card shows what a stream is on, in the theme's own
   colours. The clipper's own words come first (addendum 4.14), so the tag
   gives way first, down to its icon and a letter or two (44px), and only
   then is the title cut: the title does not shrink beside a tag, it is only
   capped at what the smallest tag leaves of the line. The tag is never more
   than 45% of the line; its whole name is in its tooltip and the card's
   label. */
.vntv-postclip-name:not(:last-child) {
    flex-shrink: 0;
    max-width: calc(100% - 50px);
}

.vntv-postclip-tag {
    display: inline-flex;
    flex: 0 1 auto;
    align-items: center;
    gap: 3px;
    min-width: 44px;
    max-width: 45%;
    border-radius: 4px;
    padding: 0 6px;
    background: rgba(var(--center-channel-color-rgb, 63, 67, 80), 0.08);
    font-size: 11px;
    font-weight: 600;
    line-height: 18px;
    white-space: nowrap;
}

.vntv-postclip-tag-kind {
    display: inline-flex;
    flex: 0 0 auto;
}

.vntv-postclip-tag-title {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
}

/* Who clipped it, whose stream and how long: one line when it fits, and
   otherwise broken between its parts, never inside one, with the dot at the
   end of the line rather than hanging before an ellipsis. A part too long
   for a line by itself (a very long name) is cut with an ellipsis. */
.vntv-postclip-meta {
    display: flex;
    flex-wrap: wrap;
    min-width: 0;
    margin: 0;
    color: rgba(var(--center-channel-color-rgb, 63, 67, 80), 0.75);
    font-size: 12px;
    line-height: 16px;
}

.vntv-postclip-meta > span {
    max-width: 100%;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
}

.vntv-postclip-meta > span:not(:last-child)::after {
    content: '·';
    margin: 0 5px;
}

.vntv-postclip-meta .vntv-clipper-mark {
    display: inline-block;
    margin-left: 3px;
    vertical-align: -3px;
}

.vntv-postclip-gone {
    margin: 0;
    font-size: 13px;
    font-weight: 600;
    line-height: 18px;
}

.vntv-postclip-note {
    margin: 0;
    color: rgba(var(--center-channel-color-rgb, 63, 67, 80), 0.75);
    font-size: 12px;
    line-height: 16px;
}

/* The answer to a press of Watch: always in the card, so it is announced,
   and taking no room while it says nothing. */
.vntv-postclip-body > .vntv-postclip-answer:empty {
    margin-top: -3px;
}

.vntv-postclip[data-compact="true"] .vntv-postclip-body > .vntv-postclip-answer:empty {
    margin-top: -1px;
}

.vntv-postclip-actions {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 6px 10px;
    margin-top: 2px;
}

.vntv-postclip-watch {
    display: inline-flex;
    flex: 0 0 auto;
    align-items: center;
    gap: 5px;
    height: 24px;
    margin: 0;
    border: 0;
    border-radius: 4px;
    padding: 0 10px;
    background: var(--button-bg, #1c58d9);
    color: var(--button-color, #ffffff);
    cursor: pointer;
    font-size: 12px;
    font-weight: 600;
    line-height: 1;
}

.vntv-postclip-watch:hover {
    background: rgba(var(--button-bg-rgb, 28, 88, 217), 0.88);
}

.vntv-postclip-watch:focus-visible {
    outline: none;
    box-shadow: 0 0 0 2px var(--center-channel-bg, #ffffff), 0 0 0 4px var(--button-bg, #1c58d9);
}

.vntv-postclip-watch[aria-disabled="true"] {
    cursor: default;
    opacity: 0.6;
}

/* What a card shows when it cannot draw the clip: the post's own message,
   as plain text, its two lines kept. */
.vntv-postclip-fallback {
    margin: 0;
    overflow-wrap: anywhere;
    white-space: pre-wrap;
}

/* ---- Clips: a clip link's page ----
   What /plug/.../clip/<id> shows for the moment before it sends Mattermost
   on to the team (ClipLinkRoute.tsx), in Mattermost's centre and its theme. */
.vntv.vntv-cliplink {
    align-items: center;
    justify-content: center;
    padding: 24px 16px;
    background: var(--center-channel-bg, #ffffff);
    color: rgba(var(--center-channel-color-rgb, 63, 67, 80), 0.75);
    font-size: 14px;
}

/* ---- Twitch ----
   People live on Twitch (design/twitch-live.md): their card in Live now, the
   tile that plays Twitch's own player, the theatre that gives it room, the
   Twitch row of Connected accounts and its sheet, Disconnect Twitch on the
   lock, and Twitch connections in the System Console. Everywhere in
   the panel's own colours, never Twitch's purple, and the plain word Twitch,
   but for the Twitch row's mark, which is Twitch's Glitch, unaltered, in
   the rows' grey (Victor's choice, 6 October 2026, kept for the row on
   12 November 2026). */

/* The plain word on a card, beside LIVE, with Twitch's whole audience in it.
   Ringed in white rather than red, so it reads as where the stream is, not
   as another way of saying it is live. */
.vntv-live-tag.vntv-card-tag-twitch {
    box-shadow: inset 0 0 0 1px rgba(255, 255, 255, 0.4);
}

.vntv-card-tag-count {
    display: inline-flex;
    align-items: center;
    gap: 3px;
    margin-left: 3px;
    padding-left: 5px;
    border-left: 1px solid rgba(255, 255, 255, 0.28);
    color: #e6e6e6;
}

.vntv-card-tag-count svg {
    display: block;
}

/* What Twitch lists the stream under, when nothing else says what is on:
   dressed as the What is on tag it stands in for, in plain words. */
.vntv-twitch-category {
    color: #e6e6e6;
}

/* Watch on Twitch, and Also on Twitch: a link like the title's own, which
   keeps its words whole while the title's links give way. Mattermost's own
   colour for links is overruled, as it is for .vntv-link. */
/* Underlined in the words' own colour, as on the picture, in a
   sheet and in Mattermost's popover alike; pointed at, the theme's link
   colour, and the stock ring from the keyboard, as .vntv-link has. */
.vntv a.vntv-twitch-out {
    color: inherit;
    text-decoration: underline;
    text-decoration-color: currentColor;
    text-underline-offset: 2px;
    white-space: nowrap;
    cursor: pointer;
}

.vntv a.vntv-twitch-out:hover,
.vntv a.vntv-twitch-out:focus-visible {
    color: var(--vntv-link);
}

.vntv a.vntv-twitch-out:focus-visible {
    outline: 2px solid var(--vntv-focus);
    outline-offset: 1px;
    border-radius: 2px;
}

.vntv-live-links .vntv-twitch-out {
    flex: 0 0 auto;
}

/* In the profile popover, on Mattermost's own theme: its words as the row's
   meta line, under it. */
.vntv-factrow-text .vntv-twitch-out {
    align-self: flex-start;
    font-size: 11px;
    line-height: 15px;
}

/* ---- Twitch: the tile ----
   Spanning the whole row of the grid, whatever its columns, so Twitch's
   player gets the panel's full width; and after the Broadcast Box tiles, by
   order only: moving a frame in the page reloads it. */
.vntv-tile[data-source="twitch"] {
    grid-column: 1 / -1;
    order: 1;
}

/* A container, so the bar can give up its count on a narrow tile. */
.vntv-twitch-tile {
    container: vntv-twitch-tile / inline-size;
}

/* The bar: whose, the word Twitch, the audience, and the tile's controls,
   in the page above the player rather than over it. Focus rings stay inside
   it. */
.vntv-twitch-bar {
    display: flex;
    align-items: center;
    gap: 6px;
    min-width: 0;
    height: 38px;
    padding: 0 4px 0 10px;
    border-bottom: 1px solid var(--vntv-line);
    background: var(--vntv-bg);
}

.vntv-twitch-name {
    flex: 0 1 auto;
    min-width: 0;
    overflow: hidden;
    color: var(--vntv-fg);
    font-family: var(--vntv-display);
    font-size: 13px;
    font-weight: 600;
    line-height: 17px;
    text-overflow: ellipsis;
    white-space: nowrap;
}

/* On the panel, not on a picture: the skin's own tag, the same on
   every theme, rather than the picture's scrim and ring. */
.vntv-twitch-bar .vntv-card-tag-twitch,
.vntv-twitch-theatre-head .vntv-card-tag-twitch {
    border-radius: 4px;
    background: var(--vntv-fill-hover);
    color: var(--vntv-fg);
    box-shadow: none;
}

.vntv-twitch-bar .vntv-card-tag-twitch {
    flex: 0 0 auto;
}

.vntv-twitch-count {
    flex: 0 0 auto;
    color: var(--vntv-second);
    font-size: 11px;
    white-space: nowrap;
    font-variant-numeric: tabular-nums;
}

@container vntv-twitch-tile (max-width: 479px) {
    .vntv-twitch-count {
        display: none;
    }
}

.vntv-twitch-tools {
    display: flex;
    flex: 0 0 auto;
    align-items: center;
    gap: 2px;
    margin-left: auto;
}

.vntv-twitch-button {
    display: inline-flex;
    flex: 0 0 auto;
    align-items: center;
    justify-content: center;
    width: 30px;
    height: 30px;
    margin: 0;
    padding: 0;
    border: none;
    border-radius: 4px;
    background: transparent;
    color: var(--vntv-icon);
    cursor: pointer;
    transition: background-color 120ms ease, color 120ms ease;
}

.vntv a.vntv-twitch-button {
    color: var(--vntv-icon);
}

.vntv-twitch-button svg {
    display: block;
}

.vntv-twitch-button[aria-pressed="true"] {
    background: rgba(var(--vntv-accent-rgb), 0.08);
    color: var(--vntv-accent);
}

.vntv-twitch-button:focus-visible,
.vntv a.vntv-twitch-button:focus-visible {
    outline: 2px solid var(--vntv-focus);
    outline-offset: -2px;
}

.vntv-twitch-button:disabled {
    cursor: default;
    opacity: 0.45;
}

/* The stage: the player, as wide as the tile, at 16:9 and never under
   Twitch's 300; or a plate in its place. */
.vntv-twitch-stage {
    position: relative;
    width: 100%;
    min-width: 0;
    overflow: hidden;
    background: #000000;
    line-height: 0;
}

.vntv-twitch-frame {
    display: block;
    border: 0;
}

/* Where the player cannot be: its picture darkened, and why, with what can
   be done. Never over a player: in a plate's mode there is no frame. */
.vntv-twitch-plate {
    position: relative;
    display: flex;
    align-items: center;
    justify-content: center;
    width: 100%;
    aspect-ratio: 16 / 9;
    max-height: var(--vntv-tile-max, 55vh);
    overflow: hidden;
    background: #0b0b0c;
    line-height: normal;
}

.vntv-twitch-plate-picture {
    position: absolute;
    inset: 0;
    width: 100%;
    height: 100%;
    object-fit: cover;
    filter: brightness(0.32) saturate(0.85);
}

.vntv-twitch-plate-words {
    position: relative;
    z-index: 1;
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 8px;
    max-width: 88%;
    text-align: center;
}

.vntv-twitch-plate-text {
    color: #ffffff;
    font-size: 13px;
    font-weight: 600;
    line-height: 1.4;
    text-shadow: 0 1px 3px rgba(0, 0, 0, 0.9);
}

.vntv-twitch-plate-reason {
    color: #c9c9c9;
    font-size: 12px;
    line-height: 1.4;
    text-shadow: 0 1px 3px rgba(0, 0, 0, 0.9);
}

.vntv-twitch-plate .vntv-pill {
    border-color: rgba(255, 255, 255, 0.55);
    background: rgba(0, 0, 0, 0.55);
    color: #ffffff;
}

.vntv a.vntv-pill.vntv-twitch-out {
    color: #ffffff;
    text-decoration: none;
}

/* Being switched off: the player has gone at once, with its sound, and the
   plate fades with the tile. */
.vntv-twitch-tile[data-closing="true"] .vntv-twitch-stage {
    opacity: 0.4;
}

/* ---- Twitch: the theatre ----
   The modal's backdrop and card, sized by twitchLogic.ts theatreSize: the
   card as wide as the player, which leaves 15px of the window either side. */
.vntv-modal.vntv-twitch-theatre {
    padding: 24px 15px;
}

/* A stock dialog: the page dimmed as stock dims it, the card the
   theme's ground and words, a 1px line, radius 12px and stock's elevation.
   Only the stage, which is Twitch's picture, stays black. */
.vntv.vntv-modal.vntv-twitch-theatre {
    background: rgba(0, 0, 0, 0.64);
    color: var(--vntv-fg);
}

.vntv-modal-card.vntv-twitch-theatre-card {
    max-width: none;
    border: 1px solid var(--vntv-line);
    border-radius: 12px;
    background: var(--vntv-bg);
    color: var(--vntv-fg);
    box-shadow: var(--elevation-6, 0 20px 32px 0 rgba(0, 0, 0, 0.12));
}

.vntv-twitch-theatre-head.vntv-modal-head {
    border-bottom: 1px solid var(--vntv-line);
}

.vntv-twitch-theatre-card .vntv-modal-close {
    border: none;
    border-radius: 4px;
    background: transparent;
    color: var(--vntv-icon);
}

.vntv-twitch-theatre-card .vntv-modal-close:focus-visible {
    outline: none;
    box-shadow: 0 0 0 2px var(--vntv-focus);
}

.vntv-modal-card.vntv-twitch-theatre-card:focus {
    outline: none;
}

.vntv-twitch-theatre-head {
    min-width: 0;
}

.vntv-modal-title.vntv-twitch-theatre-title {
    min-width: 0;
    color: var(--vntv-fg);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
}

.vntv-twitch-theatre-head .vntv-card-tag-twitch {
    flex: 0 0 auto;
}

.vntv-twitch-theatre-head .vntv-twitch-out {
    flex: 0 0 auto;
    color: var(--vntv-second);
    font-size: 12px;
}

.vntv-twitch-theatre-stage {
    background: #000000;
    line-height: 0;
}

/* ---- Twitch: the footer ----
   Since 3.4.0 Twitch has no tool in the bar: it is a row of Connected
   accounts in the relink key's sheet (AccountSheet.tsx), with its sheet
   drawn inside that one. The dot is the live dot, in LIVE's red on that
   row while the reader is live on Twitch, and in the sheet's Connected
   step. */
.vntv-twitch-dot {
    display: inline-block;
    flex: 0 0 auto;
    width: 7px;
    height: 7px;
    border-radius: 50%;
    background: rgba(var(--vntv-fg-rgb), 0.32);
}

.vntv-twitch-live .vntv-twitch-dot {
    background: var(--vntv-live);
}

/* The sheet. Focused by script as it opens, never by Tab, so no ring. */
.vntv-twitch-sheet:focus {
    outline: none;
}

/* The words that take focus when the button that had it goes with its
   phase (Waiting, Disconnected): somewhere to be, not something to press. */
.vntv-twitch-sheet .vntv-twitch-note:focus {
    outline: none;
}

.vntv-twitch-confirm {
    display: flex;
    flex-direction: column;
    gap: 10px;
    min-width: 0;
}

.vntv-twitch-note {
    margin: 0;
    color: var(--vntv-second);
    font-size: 12px;
    line-height: 1.5;
}

.vntv-twitch-note b {
    color: var(--vntv-fg);
    font-weight: 600;
}

.vntv-twitch-muted {
    color: var(--vntv-icon);
}

.vntv-twitch-error,
.vntv-twitch-note[data-tone="error"] {
    color: var(--vntv-danger);
}

.vntv-twitch-who {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: 4px 10px;
    min-width: 0;
    margin: 0;
    color: var(--vntv-fg);
    font-size: 13px;
    line-height: 1.45;
}

.vntv-twitch-who-name {
    min-width: 0;
    overflow: hidden;
    font-weight: 600;
    text-overflow: ellipsis;
}

.vntv-twitch-live {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    color: var(--vntv-live);
    font-family: var(--vntv-display);
    font-size: 10px;
    font-weight: 600;
    letter-spacing: 0.12em;
    text-transform: uppercase;
}

/* Disconnect's first press says in words what the second does, as a
   destructive action: Stock's filled danger button, as every armed
   destructive action is. */
.vntv-twitch-disconnect[data-armed="true"] {
    border-color: transparent;
    background: var(--vntv-danger-fill);
    color: var(--vntv-on-danger);
}

/* At rest in the Twitch sheet, Disconnect is a destructive action:
   stock's danger tertiary, not the accent's. */
.vntv-sheet-action.vntv-twitch-disconnect:not([data-armed="true"]) {
    background: rgba(var(--vntv-danger-rgb), 0.08);
    color: var(--vntv-danger);
}

/* The lock's footnote of it gives its words the panel's colour when it is
   pointed at (the lock screen's rules); armed, they stay the error colour:
   one class more than the lock's rule, so it wins wherever it sits. */
.vntv-token-dock .vntv-twitch-lock .vntv-textkey.vntv-twitch-disconnect[data-armed="true"] {
    color: var(--vntv-on-danger);
}

/* One armed look for every two-press destructive action: the key
   itself becomes stock's filled danger button, its question as its label,
   in sentence case in the page's own face, radius 4. These keys are small
   condensed capitals at rest; armed, they are not. */
.vntv .vntv-textkey.vntv-twitch-disconnect[data-armed="true"],
.vntv .vntv-textkey.vntv-acctkey-key[data-armed="true"] {
    padding: 0 12px;
    font-family: inherit;
    font-size: 12px;
    letter-spacing: normal;
    text-transform: none;
}

/* The takeover notice sits over the bar like the bar's other warning; its
   OK keeps its size. */
.vntv-twitch-lost .vntv-sheet-action {
    flex: 0 0 auto;
}

/* Disconnect Twitch for somebody who is not linked, with what it says once
   pressed under it: the key's words on the left edge of the words around
   it, its padding hung outside. The lock sets it out its own way (the lock
   screen section above); the let-in footer's is below. */
.vntv-twitch-lock {
    width: 100%;
    max-width: 320px;
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: 6px;
    margin-top: 14px;
}

.vntv-twitch-lock .vntv-textkey {
    margin-left: -9px;
}

.vntv-twitch-lock .vntv-twitch-note {
    font-size: 13px;
    line-height: 1.45;
}

.vntv-twitch-lock .vntv-twitch-note:focus {
    outline: none;
}

/* The same key in the footer of somebody let in to watch, on a line of its
   own over the WATCH ONLY bar, in the watch-only note's small grey words. */
.vntv-twitch-lock.vntv-twitch-watchonly {
    max-width: none;
    gap: 2px;
    margin: 0 0 6px;
    padding: 0 6px;
}

.vntv-twitch-watchonly .vntv-twitch-note {
    font-size: 12px;
}

/* ---- 3.4.0: Disconnect for somebody not linked ----
   design/3.4.0.md 5.5, Victor's P3 pick B (AccountsKey.tsx): with Twitch
   alone it is Disconnect Twitch, above, unchanged. With any other service,
   one line in the same place: "Disconnect Steam", or "Disconnect" and each
   connected service's name as a key of its own; pressed once, the whole
   line is that one service's two-press words, never cut short. Laid out
   like Disconnect Twitch, with what it says once pressed under it: the lock
   sets it out its own way, and so does the WATCH ONLY line (below). */
.vntv-acctkey {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: 6px;
    width: 100%;
    max-width: 320px;
    margin-top: 14px;
}

.vntv-acctkey-line {
    display: inline-flex;
    flex-wrap: nowrap;
    align-items: center;
    min-width: 0;
}

/* The word "Disconnect" before the names: the keys' own letters, a step
   quieter, and not a key. */
.vntv-acctkey-label {
    padding: 0 4px 0 0;
    color: var(--vntv-icon);
    font-family: var(--vntv-display);
    font-size: 11px;
    font-weight: 600;
    letter-spacing: 0.12em;
    text-transform: uppercase;
    white-space: nowrap;
}

.vntv-acctkey-dot {
    color: rgba(var(--vntv-fg-rgb), 0.48);
    font-size: 11px;
}

.vntv-acctkey-line .vntv-acctkey-word {
    padding: 0 6px;
}

/* Pressed once, the key says in words what the second press does, as a
   destructive action, as Disconnect Twitch's does. */
.vntv-acctkey-key[data-armed="true"] {
    border-color: transparent;
    background: var(--vntv-danger-fill);
    color: var(--vntv-on-danger);
}

.vntv-acctkey-note:focus {
    outline: none;
}

/* Where there is no bar to sit beside (the plugin not set up): centred under
   the words, like them. */
.vntv-lock-column .vntv-acctkey {
    align-items: center;
    margin-top: 16px;
    text-align: center;
}

/* On the lock, on its own line under the bar at the right, like a footnote:
   Disconnect Twitch's smaller words and tall-enough keys, the padding hung
   outside the bar's edge; armed, square with it. Its note goes in the
   dock's row at the top, and is kept in the page while it says nothing, so
   it is there to take focus when a key goes with its service. */
.vntv-token-dock .vntv-acctkey {
    display: contents;
}

.vntv-token-dock .vntv-acctkey-line {
    grid-area: key;
    justify-self: end;
    margin: 0 -4px 4px 0;
}

.vntv-token-dock .vntv-acctkey-line[data-armed="true"] {
    margin-right: 0;
}

.vntv-token-dock .vntv-acctkey .vntv-textkey {
    height: 32px;
    padding: 0 6px;
    color: var(--vntv-second);
    font-size: 10px;
}

.vntv-token-dock .vntv-acctkey .vntv-textkey:hover {
    color: var(--vntv-fg);
}

/* Armed, the filled danger button's words, as everywhere else it is
   armed. */
.vntv-token-dock .vntv-acctkey .vntv-textkey[data-armed="true"] {
    color: var(--vntv-on-danger);
}

.vntv-token-dock .vntv-acctkey-label,
.vntv-token-dock .vntv-acctkey-dot {
    font-size: 10px;
}

.vntv-token-dock .vntv-acctkey .vntv-acctkey-note {
    grid-area: note;
    padding: 0 6px;
    font-size: 12px;
    line-height: 1.5;
}

.vntv-token-dock .vntv-acctkey .vntv-acctkey-note:not(:empty) {
    margin-bottom: 8px;
}

/* Over the WATCH ONLY bar, on a line of its own, in the watch-only note's
   small grey words: the first word on the edge of the words around it, a
   key's padding hung outside. */
.vntv-acctkey.vntv-acctkey-watchonly {
    max-width: none;
    gap: 2px;
    margin: 0 0 6px;
    padding: 0 6px;
}

.vntv-acctkey-watchonly .vntv-acctkey-line[data-single="true"],
.vntv-acctkey-watchonly .vntv-acctkey-line[data-armed="true"] {
    margin-left: -9px;
}

.vntv-acctkey-watchonly .vntv-acctkey-note {
    font-size: 12px;
}

/* The takeover notices' slot over the linked bar (AccountNotices.tsx): no
   box of its own, so a notice sits in the footer as Twitch's always has. */
.vntv-account-notices {
    display: contents;
}

/* What stays in the database, said under Delete all on the Steam and Hytale
   console cards, a little clear of its pills (AdminAccountParts.tsx). */
.vntv-admin .vntv-account-admin-after {
    margin-top: 8px;
}

/* ---- Connected accounts (design/3.4.0.md 5.2-5.3, Victor's P1 pick B) ----
   Twitch, Steam and Hytale as rows under the token bar and Unlink in the
   relink key's sheet (AccountSheet.tsx). A row is a grid: the mark, the
   service's word, whose account it is and what Steam says, and one button.
   The word's column is one width for every row, so the names line up under
   each other. A row whose one line would cut its name or its status
   (measured by AccountSheet.tsx, as data-wrap) and a row whose Disconnect
   is armed put the status on a second, muted line under the word, and the
   name keeps the first line's room. */
.vntv-accounts {
    display: flex;
    flex-direction: column;
    min-width: 0;
    container: vntv-accounts / inline-size;
}

.vntv-accounts:focus,
.vntv-account-sub:focus {
    outline: none;
}

/* Under the token bar and Unlink: a hairline, then the list's heading in
   the words of the token bar's own label above it. */
.vntv-accounts-under {
    margin-top: 2px;
    padding-top: 12px;
    border-top: 1px solid var(--vntv-line);
}

.vntv-accounts-heading {
    margin: 0 0 2px;
    color: var(--vntv-second);
    font-family: var(--vntv-display);
    font-size: 11px;
    font-weight: 600;
    letter-spacing: 0.14em;
    line-height: 14px;
    text-transform: uppercase;
}

.vntv-account-row {
    display: grid;
    grid-template-columns: 16px 46px minmax(0, 1fr) auto;
    grid-template-areas: "mark word main act";
    align-items: center;
    column-gap: 8px;
    row-gap: 1px;
    min-width: 0;
    min-height: 40px;
    box-sizing: border-box;
    padding: 6px 0;
    border-top: 1px solid var(--vntv-line);
}

.vntv-accounts-heading + .vntv-account-row,
.vntv-account-row:first-child {
    border-top: none;
}

/* The mark, grey; lit once the account is connected. The Glitch is drawn
   as it is, scaled only as a whole and with nothing over it. */
.vntv-account-mark {
    grid-area: mark;
    display: flex;
    align-items: center;
    justify-content: center;
    color: rgba(var(--vntv-fg-rgb), 0.48);
    line-height: 0;
}

.vntv-account-mark svg {
    display: block;
    flex: 0 0 auto;
}

.vntv-account-row[data-connected="true"] .vntv-account-mark {
    color: var(--vntv-icon);
}

.vntv-account-word {
    grid-area: word;
    overflow: hidden;
    color: var(--vntv-fg);
    font-family: var(--vntv-display);
    font-size: 11px;
    font-weight: 600;
    letter-spacing: 0.12em;
    line-height: 14px;
    text-transform: uppercase;
    white-space: nowrap;
}

/* Whose account, then what Steam says, on one line: the name keeps up to
   three fifths of it and the status gives way first. */
.vntv-account-main {
    grid-area: main;
    display: flex;
    align-items: baseline;
    min-width: 0;
    overflow: hidden;
    white-space: nowrap;
}

.vntv-account-name {
    flex: 0 0 auto;
    max-width: 60%;
    min-width: 0;
    overflow: hidden;
    color: var(--vntv-fg);
    font-size: 13px;
    font-weight: 600;
    line-height: 18px;
    text-overflow: ellipsis;
}

/* A name with nothing after it (Hytale's) has the line to itself, and is
   the one thing there to give way, with an ellipsis. */
.vntv-account-name:only-child {
    flex-shrink: 1;
    max-width: none;
}

.vntv-account-sub {
    flex: 0 1 auto;
    min-width: 0;
    overflow: hidden;
    color: var(--vntv-second);
    font-size: 12px;
    line-height: 18px;
    text-overflow: ellipsis;
}

.vntv-account-name + .vntv-account-sub::before {
    content: '·';
    margin: 0 6px;
    color: rgba(var(--vntv-fg-rgb), 0.48);
}

/* Live on Twitch: the old tool's red dot, said on the row in red
   capitals. */
.vntv-account-sub[data-tone="live"] {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    color: var(--vntv-live);
    font-family: var(--vntv-display);
    font-size: 10px;
    font-weight: 600;
    letter-spacing: 0.12em;
    text-transform: uppercase;
}

.vntv-account-sub[data-tone="live"] .vntv-twitch-dot {
    background: var(--vntv-live);
}

/* A failed Disconnect, and why Connect is not there (admins): in red,
   free to wrap. Disconnect's first press says its question in the
   button itself, as everywhere else, not on a line under the row. */
.vntv-account-sub[data-tone="problem"] {
    color: var(--vntv-danger);
    white-space: normal;
    line-height: 1.4;
}

/* Not available, and the words after a Disconnect: no button, so the words
   take its column too, and wrap. */
.vntv-account-row[data-unavailable="true"] .vntv-account-main,
.vntv-account-row[data-said="true"] .vntv-account-main {
    grid-column: 3 / 5;
    white-space: normal;
}

.vntv-account-row[data-said="true"] .vntv-account-sub {
    color: var(--vntv-second);
    line-height: 1.45;
}

.vntv-account-act {
    grid-area: act;
}

/* A destructive action: stock's danger tertiary at rest, its filled
   danger button armed. */
.vntv-account-disconnect {
    background: rgba(var(--vntv-danger-rgb), 0.08);
    color: var(--vntv-danger);
    box-shadow: none;
}

/* Armed, the button says "Disconnect Twitch? Press again" and grows
   to the left, its right edge where the first press was; where the row
   has no room for it, its words wrap inside it. */
.vntv-account-disconnect[data-armed="true"] {
    background: var(--vntv-danger-fill);
    color: var(--vntv-on-danger);
    box-shadow: none;
    height: auto;
    min-height: 32px;
    padding-top: 6px;
    padding-bottom: 6px;
    line-height: 16px;
    white-space: normal;
}

.vntv-account-disconnect:focus-visible {
    box-shadow: 0 0 0 2px var(--vntv-focus);
}

.vntv-account-disconnect[aria-disabled="true"] {
    cursor: progress;
}

/* Two lines: a failed Disconnect, at any width, or a row whose one line
   would cut something. The status, or why it failed in its place, under
   the word; why it failed is never cut short. */
.vntv-account-row[data-problem="true"],
.vntv-account-row[data-wrap="true"] {
    grid-template-areas: "mark word main act" ". sub sub sub";
}

.vntv-account-row[data-problem="true"] .vntv-account-main,
.vntv-account-row[data-wrap="true"] .vntv-account-main {
    display: contents;
}

.vntv-account-row[data-problem="true"] .vntv-account-name,
.vntv-account-row[data-wrap="true"] .vntv-account-name {
    grid-area: main;
    max-width: none;
}

.vntv-account-row[data-problem="true"] .vntv-account-sub,
.vntv-account-row[data-wrap="true"] .vntv-account-sub {
    grid-area: sub;
    line-height: 16px;
}

.vntv-account-row[data-problem="true"] .vntv-account-sub {
    white-space: normal;
}

.vntv-account-row[data-problem="true"] .vntv-account-sub::before,
.vntv-account-row[data-wrap="true"] .vntv-account-sub::before {
    content: none;
}

/* Under 300 px of content (a 304 px panel's 250) the marks give their room
   to the names, so a 16-character Hytale name is not cut; the service's
   word still says whose row it is. */
@container vntv-accounts (max-width: 299px) {
    .vntv-account-row {
        grid-template-columns: 46px minmax(0, 1fr) auto;
        grid-template-areas: "word main act";
    }

    .vntv-account-row[data-problem="true"],
    .vntv-account-row[data-wrap="true"] {
        grid-template-areas: "word main act" "sub sub sub";
    }

    .vntv-account-mark {
        display: none;
    }

    .vntv-account-row[data-unavailable="true"] .vntv-account-main,
    .vntv-account-row[data-said="true"] .vntv-account-main {
        grid-column: 2 / 4;
    }
}

/* A service's flow in the sheet's place, under a head with an arrow back
   to the list. Focused by script as it opens, never by Tab, so no ring. */
.vntv-account-flow {
    display: flex;
    flex-direction: column;
    gap: 10px;
    min-width: 0;
}

.vntv-account-flow:focus,
.vntv-account-flow .vntv-twitch-note:focus,
.vntv-sheet-heading:focus {
    outline: none;
}

/* The arrow back to the list, the twin of the head's close button:
   the one back control of every sheet (Adding a sound's too), 32px. */
.vntv-sheet-back {
    display: inline-flex;
    flex: 0 0 auto;
    align-items: center;
    justify-content: center;
    width: 32px;
    height: 32px;
    margin: -6px -4px 0 -10px;
    padding: 0;
    border: none;
    border-radius: 4px;
    background: transparent;
    color: var(--vntv-icon);
    cursor: pointer;
    transition: background-color 120ms ease, color 120ms ease;
}

.vntv-sheet-back svg {
    display: block;
}

.vntv-sheet-back:focus-visible {
    outline: none;
    box-shadow: 0 0 0 2px var(--vntv-focus);
}

/* Twitch's own sheet inside Connected accounts: no head of its own, and no
   box, since the sheet around it is the box. */
.vntv-twitch-embedded {
    display: flex;
    flex-direction: column;
    gap: 10px;
    min-width: 0;
}

@media (hover: hover) {
    .vntv-sheet-back:hover {
        background: var(--vntv-fill-hover);
        color: var(--vntv-icon-hover);
    }
}

/* ---- Twitch: the System Console ----
   Twitch connections (AdminTwitch.tsx): the address to copy, a 24/7 box on
   each row, and Delete all in the console's red. */
.vntv-access-entry.vntv-twitch-callback {
    max-width: 680px;
}

.vntv-admin .vntv-twitch-callback-url {
    font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
    font-size: 12px;
}

.vntv-admin .vntv-admin-check.vntv-twitch-allday {
    margin-top: 6px;
    color: var(--vntv-admin-quiet);
    font-size: 13px;
    line-height: 18px;
}

.vntv-twitch-deleteall {
    margin-top: 16px;
}

/* Not while armed: the armed red, filled, says the second press. */
.vntv-admin .vntv-pill[data-tone="danger"]:not([data-armed="true"]) {
    border-color: rgba(var(--vntv-admin-danger-rgb), 0.6);
    color: var(--vntv-admin-danger-text);
}

.vntv-admin button.vntv-pill[data-tone="danger"]:not([data-armed="true"]):hover:enabled:not([aria-disabled="true"]) {
    border-color: rgb(var(--vntv-admin-danger-rgb));
    background: rgba(var(--vntv-admin-danger-rgb), 0.08);
}

@media (hover: hover) {
    .vntv-twitch-button:hover:enabled,
    .vntv a.vntv-twitch-button:hover {
        background: rgba(255, 255, 255, 0.08);
        color: #ffffff;
    }
}

/* ---- 3.2.0: awards by your name, achievements ----
   design/3.2.0.md 3 and 5, as Victor picked them: the picker in the Trophies
   card's You tab, with the You tab's progress and fan switch, the
   Achievements tab by family, your own popover's takebacks, and the toast for
   a new step. On the theme throughout, like the card; the
   takebacks sit in Mattermost's own popover and take its colours. */

/* A post as it will look: Mattermost's header on the card's ground. */
.vntv-awardpick {
    display: flex;
    flex-direction: column;
    gap: 10px;
    min-width: 0;
}

.vntv-awardpick-preview {
    display: flex;
    align-items: flex-start;
    gap: 10px;
    min-width: 0;
    padding: 10px 12px;
    border: 1px solid var(--vntv-line);
    border-radius: 4px;
    background: var(--vntv-fill);
    container: vntv-awardpreview / inline-size;
}

.vntv-face.vntv-awardpick-face {
    width: 32px;
    height: 32px;
    font-size: 12px;
}

.vntv-awardpick-post {
    display: flex;
    flex: 1 1 auto;
    flex-direction: column;
    gap: 2px;
    min-width: 0;
}

.vntv-awardpick-head {
    display: flex;
    align-items: center;
    gap: 6px;
    min-width: 0;
}

/* On a narrow card the name gives way first, down to a few letters, as
   design/3.2.0.md 3.6 has it and as a post header's own name does; the
   badges, which are the point of it, never do, and the time only goes where
   there is no room for it at all. */
.vntv-awardpick-name {
    overflow: hidden;
    flex: 0 1 auto;
    min-width: 3em;
    color: var(--vntv-fg);
    font-size: 14px;
    font-weight: 600;
    text-overflow: ellipsis;
    white-space: nowrap;
}

.vntv-awardpick-badges {
    display: inline-flex;
    flex: 0 0 auto;
    align-items: center;
    gap: 3px;
}

.vntv-awardpick-badges svg {
    display: block;
}

.vntv-awardpick-time {
    flex: 0 0 auto;
    color: var(--vntv-second);
    font-size: 12px;
    white-space: nowrap;
}

/* No room for the time beside the headphones, four badges and a few letters
   of the name: it goes altogether rather than as a sliver. */
@container vntv-awardpreview (max-width: 235px) {
    .vntv-awardpick-time {
        display: none;
    }
}

.vntv-awardpick-message {
    overflow: hidden;
    color: var(--vntv-fg);
    font-size: 13px;
    text-overflow: ellipsis;
    white-space: nowrap;
}

.vntv-awardpick-note {
    margin: 0;
    color: var(--vntv-second);
    font-size: 12px;
    line-height: 1.45;
}

/* Automatic or My choice: two segments in one box, the chosen one in the
   accent. Stock's control corners and lines. */
.vntv-segments {
    display: inline-flex;
    align-self: flex-start;
    gap: 2px;
    padding: 3px;
    border: 1px solid var(--vntv-line);
    border-radius: 4px;
    background: var(--vntv-fill);
}

.vntv-segment {
    margin: 0;
    padding: 5px 14px;
    border: none;
    border-radius: 2px;
    background: transparent;
    color: var(--vntv-second);
    cursor: pointer;
    font-family: var(--vntv-display);
    font-size: 12px;
    font-weight: 600;
    letter-spacing: 0.08em;
    line-height: 1.2;
    text-transform: uppercase;
    transition: background-color 150ms ease, color 150ms ease;
}

.vntv-segment[aria-checked="true"] {
    background: var(--vntv-accent);
    color: var(--vntv-on-accent);
}

.vntv-segment:focus-visible {
    outline: none;
    box-shadow: 0 0 0 2px var(--vntv-focus);
}

.vntv-awardpick-hint {
    margin: 0;
    color: var(--vntv-second);
    font-size: 12px;
    line-height: 1.45;
}

.vntv-awardpick-failed {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 8px;
    color: var(--vntv-second);
    font-size: 12px;
}

/* Every award held, as a chip: its badge at 22px and its name. Four to a
   row on a wide card, two below a window of about 420 px. */
.vntv-awardpick-chips {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(132px, 1fr));
    gap: 12px 8px;
    margin: 4px 0 0;
    padding: 0;
    list-style: none;
}

/* While the choice is loading the chips keep their room and cannot be
   pressed. */
.vntv-awardpick-chips[data-loading="true"] {
    opacity: 0.5;
    pointer-events: none;
}

.vntv-awardpick-item {
    position: relative;
    min-width: 0;
}

.vntv-awardchip {
    display: flex;
    align-items: center;
    gap: 8px;
    box-sizing: border-box;
    width: 100%;
    min-height: 46px;
    margin: 0;
    padding: 6px 9px 6px 8px;
    border: 1px solid var(--vntv-line);
    border-radius: 4px;
    background: var(--vntv-fill);
    color: var(--vntv-second);
    cursor: pointer;
    font: inherit;
    font-size: 12px;
    line-height: 1.3;
    text-align: left;
    transition: border-color 150ms ease, background-color 150ms ease, opacity 150ms ease;
}

.vntv-awardchip[aria-pressed="true"] {
    border-color: var(--vntv-accent);
    background: rgba(var(--vntv-accent-rgb), 0.08);
    color: var(--vntv-fg);
}

.vntv-awardchip:focus-visible {
    outline: none;
    box-shadow: 0 0 0 2px var(--vntv-focus);
}

/* Automatic: the four it draws are lit, quietly, and the rest wait. Nothing
   can be pressed until My choice. */
.vntv-awardchip[data-auto] {
    cursor: default;
}

.vntv-awardchip[data-auto="shown"] {
    border-color: var(--vntv-line-focus);
    background: var(--vntv-fill-hover);
    color: var(--vntv-fg);
}

/* Waiting in Automatic, or four chosen and no room: the chip steps back but
   a NEW on it does not, so what is new still shows. Its words stay
   readable (they say what there is to choose): the second-rank 0.64 on no
   fill, and only the art fades and loses its colour. */
.vntv-awardchip[data-auto="hidden"],
.vntv-awardchip[aria-disabled="true"]:not([data-auto]) {
    background: transparent;
    color: rgba(var(--vntv-fg-rgb), 0.64);
}

.vntv-awardchip[data-auto="hidden"] .vntv-awardchip-art,
.vntv-awardchip[aria-disabled="true"]:not([data-auto]) .vntv-awardchip-art {
    opacity: 0.5;
    filter: grayscale(1);
}

.vntv-awardchip[aria-disabled="true"]:not([data-auto]) {
    cursor: default;
}

/* Chosen but not held now: kept where it was put, dimmed and dashed. */
.vntv-awardchip[data-held="false"] {
    border-style: dashed;
    color: var(--vntv-icon);
}

.vntv-awardchip[data-held="false"] .vntv-awardchip-art {
    opacity: 0.45;
    filter: grayscale(1);
}

.vntv-awardchip-art {
    display: flex;
    flex: 0 0 auto;
    align-items: center;
    justify-content: center;
    width: 22px;
    height: 22px;
}

.vntv-awardchip-art svg {
    display: block;
}

.vntv-awardchip-gone {
    width: 18px;
    height: 18px;
    border: 1.5px dashed var(--vntv-line-focus);
    border-radius: 50%;
}

.vntv-awardchip-words {
    display: flex;
    flex: 1 1 auto;
    flex-direction: column;
    min-width: 0;
}

/* The whole name, on as many lines as it needs: cut to two, "Monthly
   champion ×5" ended "champio…" on a phone and lost the count that is the
   point of it. */
.vntv-awardchip-label {
    overflow-wrap: break-word;
}

.vntv-awardchip-sub {
    color: var(--vntv-second);
    font-size: 11px;
}

/* Its place in the line, 1 to 4. */
.vntv-awardchip-place {
    display: inline-flex;
    flex: 0 0 auto;
    align-items: center;
    justify-content: center;
    width: 19px;
    height: 19px;
    border-radius: 50%;
    background: var(--vntv-accent);
    color: var(--vntv-on-accent);
    font-family: var(--vntv-display);
    font-size: 11px;
    font-weight: 600;
    line-height: 1;
}

/* Won since the picker was last opened, and not chosen yet. */
.vntv-awardchip-new {
    position: absolute;
    top: -7px;
    right: 8px;
    padding: 1px 6px;
    border-radius: 999px;
    background: var(--vntv-accent);
    box-shadow: 0 0 0 2px var(--vntv-bg);
    color: var(--vntv-on-accent);
    font-family: var(--vntv-display);
    font-size: 9px;
    font-weight: 600;
    letter-spacing: 0.14em;
    line-height: 12px;
    text-transform: uppercase;
}

/* Earlier and later: a small pair on the chosen chip's top edge, there while
   it is pointed at or has focus (and Alt with the arrows from the chip). */
.vntv-awardchip-moves {
    position: absolute;
    top: -9px;
    right: 6px;
    display: none;
    gap: 2px;
    padding: 1px;
    border: 1px solid var(--vntv-line);
    border-radius: 999px;
    background: var(--vntv-bg);
    box-shadow: var(--elevation-1, 0 2px 3px 0 rgba(0, 0, 0, 0.08));
}

.vntv-awardpick-item[data-picked="true"]:hover .vntv-awardchip-moves,
.vntv-awardpick-item[data-picked="true"]:focus-within .vntv-awardchip-moves {
    display: inline-flex;
}

.vntv-awardchip-move {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 18px;
    height: 16px;
    margin: 0;
    padding: 0;
    border: none;
    border-radius: 999px;
    background: transparent;
    color: var(--vntv-icon);
    cursor: pointer;
}

.vntv-awardchip-move:not(:disabled):hover {
    background: var(--vntv-fill-hover);
    color: var(--vntv-icon-hover);
}

.vntv-awardchip-move[data-way="earlier"] svg {
    transform: rotate(180deg);
}

.vntv-awardchip-move:disabled {
    cursor: default;
    opacity: 0.3;
}

.vntv-awardchip-move:focus-visible {
    outline: none;
    box-shadow: 0 0 0 2px var(--vntv-focus);
}

.vntv-awardpick-status {
    min-height: 15px;
    margin: 0;
    color: var(--vntv-second);
    font-size: 11px;
}

/* ---- 3.2.0: the You tab ----
   What shows by your name, then a thin rule and your achievements, then the
   biggest-fan switch. */
.vntv-you {
    display: flex;
    flex-direction: column;
    gap: 18px;
    min-width: 0;
}

.vntv-you-section {
    display: flex;
    flex-direction: column;
    gap: 10px;
    min-width: 0;
}

.vntv-you-section + .vntv-you-section {
    padding-top: 16px;
    border-top: 1px solid var(--vntv-line);
}

.vntv-you-progress {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
    gap: 10px 22px;
    margin: 0;
    padding: 0;
    list-style: none;
}

.vntv-you-step {
    display: flex;
    align-items: center;
    gap: 10px;
    min-width: 0;
}

.vntv-you-coin {
    display: flex;
    flex: 0 0 auto;
    line-height: 0;
}

/* A family not started: its first coin, faint and grey. */
.vntv-you-step[data-none="true"] .vntv-you-coin {
    opacity: 0.35;
    filter: grayscale(1);
}

.vntv-you-words {
    display: flex;
    flex: 1 1 auto;
    flex-direction: column;
    gap: 4px;
    min-width: 0;
}

.vntv-you-name {
    overflow: hidden;
    color: var(--vntv-fg);
    font-family: var(--vntv-display);
    font-size: 13px;
    font-weight: 600;
    letter-spacing: 0.02em;
    text-overflow: ellipsis;
    white-space: nowrap;
}

.vntv-you-step[data-none="true"] .vntv-you-name {
    color: var(--vntv-second);
}

.vntv-you-bar {
    display: block;
    overflow: hidden;
    height: 4px;
    border-radius: 999px;
    background: var(--vntv-fill-hover);
}

/* Progress in the accent. */
.vntv-you-fill {
    display: block;
    height: 100%;
    border-radius: 999px;
    background: var(--vntv-accent);
}

/* Until the reader's own figures arrive, or when they cannot be had: the
   steps the snapshot says are held, with no bar, in the room the bar will
   take, so nothing moves when the figures come. An empty bar would read as
   nothing done. */
.vntv-you-progress[data-loading="true"] .vntv-you-bar {
    visibility: hidden;
}

.vntv-you-meta {
    color: var(--vntv-second);
    font-size: 11px;
    font-variant-numeric: tabular-nums;
}

/* ---- 3.2.0: the Achievements tab ----
   By family: each family's ladder, top step first, with the people on each
   step. */
.vntv-ach-families {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(270px, 1fr));
    gap: 22px;
}

.vntv-ach-family {
    min-width: 0;
}

.vntv-ach-family-head {
    display: flex;
    align-items: center;
    gap: 10px;
    margin-bottom: 8px;
}

.vntv-ach-family-coin {
    display: flex;
    flex: 0 0 auto;
    line-height: 0;
}

.vntv-ach-family-words {
    min-width: 0;
}

.vntv-ach-ladder {
    display: flex;
    flex-direction: column;
    gap: 2px;
    margin: 0;
    padding: 0;
    list-style: none;
}

.vntv-ach-rung {
    display: grid;
    grid-template-columns: 18px 62px minmax(0, 1fr);
    align-items: center;
    gap: 8px;
    min-height: 30px;
    padding: 3px 8px;
    border: 1px solid transparent;
    border-radius: 4px;
}

/* Your own step as your own row on a board is marked. */
.vntv-ach-rung[data-me="true"] {
    border-color: rgba(var(--vntv-accent-rgb), 0.24);
    background: rgba(var(--vntv-accent-rgb), 0.08);
}

.vntv-ach-rung-coin {
    display: flex;
    line-height: 0;
}

.vntv-ach-rung-step {
    color: var(--vntv-fg);
    font-size: 12px;
    font-variant-numeric: tabular-nums;
    white-space: nowrap;
}

/* A step nobody has reached yet: on the ladder, quieter than the steps
   people stand on but still readable, because what comes next is half of
   what a ladder is for. The words are the second-rank words (about 4.5:1
   on the card) rather than fainter, which read at under 3:1. */
.vntv-ach-rung[data-empty="true"] .vntv-ach-rung-coin {
    opacity: 0.45;
    filter: saturate(0.6);
}

.vntv-ach-rung[data-empty="true"] .vntv-ach-rung-step {
    color: var(--vntv-second);
}

.vntv-ach-rung-people {
    display: flex;
    flex-wrap: wrap;
    gap: 4px 12px;
    min-width: 0;
}

.vntv-ach-rung-none {
    color: var(--vntv-line-focus);
    font-size: 12px;
}

.vntv-ach-person {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    min-width: 0;
    color: var(--vntv-fg);
    font-size: 12.5px;
    white-space: nowrap;
}

.vntv-ach-person[data-me="true"] {
    font-weight: 600;
}

.vntv-face.vntv-ach-face {
    width: 18px;
    height: 18px;
    font-size: 8px;
}

/* ---- 3.2.0: the achievements in a profile ----
   One row, its coins and their steps wrapping under the title. */
.vntv-factrow.vntv-factrow-ach {
    align-items: flex-start;
}

.vntv-factrow-ach .vntv-factrow-mark {
    padding-top: 1px;
}

.vntv-factrow-steps {
    display: flex;
    flex-wrap: wrap;
    gap: 2px 10px;
    margin-top: 3px;
    font-size: 11.5px;
    line-height: 22px;
}

.vntv-factrow-step {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    white-space: nowrap;
}

.vntv-factrow-step svg {
    display: block;
    flex: 0 0 auto;
}

/* ---- 3.2.0: your own popover's takebacks ----
   Inside Mattermost's own profile popover, on whatever theme the reader has:
   no colour of their own, edges drawn in the theme's text colour. */
.vntv-takebacks {
    display: flex;
    flex-direction: column;
    align-items: stretch;
    gap: 5px;
    width: 100%;
    max-width: 260px;
    padding: 4px 0;
    color: inherit;
}

.vntv-takeback {
    display: flex;
    align-items: center;
    gap: 8px;
    box-sizing: border-box;
    width: 100%;
    min-height: 32px;
    margin: 0;
    padding: 5px 10px;
    border: 1px solid var(--vntv-line);
    border-radius: 4px;
    background: transparent;
    color: inherit;
    cursor: pointer;
    font: inherit;
    font-size: 12px;
    line-height: 1.35;
    text-align: left;
}

.vntv-takeback:hover {
    background: var(--vntv-fill-hover);
}

/* A takeback deletes or gives up something: stock's danger tertiary at
   rest, as every destructive action is. */
.vntv-takeback:not([data-armed="true"]) {
    border-color: transparent;
    background: rgba(var(--vntv-danger-rgb), 0.08);
    color: var(--vntv-danger);
}

.vntv-takeback:not([data-armed="true"]):hover {
    background: rgba(var(--vntv-danger-rgb), 0.16);
}

.vntv-takeback:focus-visible {
    outline: none;
    box-shadow: 0 0 0 2px var(--vntv-focus);
}

/* Armed, a takeback is a destructive action one press away, so it
   is drawn as stock draws one: the theme's error colour. */
.vntv-takeback[data-armed="true"] {
    border-color: transparent;
    background: var(--vntv-danger-fill);
    color: var(--vntv-on-danger);
}

/* Each takeback with what it said, under its own button. */
.vntv-takeback-slot {
    display: flex;
    flex-direction: column;
    gap: 4px;
}

.vntv-takeback-said {
    margin: 0;
    font-size: 11.5px;
    line-height: 1.4;
    opacity: 0.8;
}

/* Done, where the button was: said as plainly as the button it replaces. */
.vntv-takeback-said[data-tone="done"] {
    font-size: 12px;
    opacity: 0.9;
}

.vntv-takeback-said:empty {
    display: none;
}

/* ---- 3.2.0: the toast for a new achievement ----
   Over the footer's bar, on its own glass, for six seconds; it rises out of
   the bar like the sheets and fades away. It covers nothing that can be
   pressed for long, and is never pressed itself. */
.vntv-achtoast-host {
    position: absolute;
    right: 0;
    bottom: calc(100% + 8px);
    left: 0;
    z-index: 3;
    display: flex;
    justify-content: center;
    pointer-events: none;
}

.vntv-achtoast {
    display: flex;
    align-items: center;
    gap: 11px;
    box-sizing: border-box;
    width: 100%;
    min-width: 0;
    padding: 9px 14px 9px 10px;

    /* A stock surface over the bar, as the sheets are. */
    border: 1px solid var(--vntv-line);
    border-radius: 4px;
    background: var(--vntv-bg);
    color: var(--vntv-fg);
    box-shadow: var(--elevation-4, 0 8px 24px 0 rgba(0, 0, 0, 0.12));
    animation: vntv-achtoast-in 280ms cubic-bezier(0.22, 1, 0.36, 1) both;
}

.vntv-achtoast[data-leaving="true"] {
    animation: vntv-achtoast-out 300ms ease forwards;
}

@keyframes vntv-achtoast-in {
    from { opacity: 0; transform: translateY(10px); }
    to { opacity: 1; transform: translateY(0); }
}

@keyframes vntv-achtoast-out {
    from { opacity: 1; }
    to { opacity: 0; transform: translateY(4px); }
}

.vntv-achtoast-coin {
    display: flex;
    flex: 0 0 auto;
    line-height: 0;
}

.vntv-achtoast-words {
    display: flex;
    flex: 1 1 auto;
    flex-direction: column;
    gap: 2px;
    min-width: 0;
}

.vntv-achtoast-eyebrow {
    color: var(--vntv-second);
    font-family: var(--vntv-display);
    font-size: 9.5px;
    font-weight: 600;
    letter-spacing: 0.16em;
    line-height: 12px;
    text-transform: uppercase;
}

.vntv-achtoast-name {
    overflow: hidden;
    color: var(--vntv-fg);
    font-size: 13px;
    line-height: 17px;
    text-overflow: ellipsis;
    white-space: nowrap;
}

/* ---- 3.2.0: the biggest fan (design/3.2.0.md 4, Victor's P3 pick) ----
   Who has watched a streamer most, all time. Gold, as the awards with one
   holder are: a gold ring round the fan's face wherever it stands for the
   title, and the gold heart they may wear beside their own name. */

/* The quiet line: who has watched them most, under the title, dimmer than the
   title above it: the fan's face in a gold ring, then "Biggest fan · Bob".
   Last in the info, so it rises into the picture and never moves the name
   or the title. With room, a line of its own.

   The small two-up card has no line to spare on a card that says what is
   on: one more runs into the tags along the top. So there it is the same
   face as "Fan · Bob", always at the right-hand end of the links line, and
   it gives way to the links rather than cutting them. The tail wraps, so a
   fan that does not fit beside the links goes under them; on a card that
   says what is on, the tail is one line high, and a fan that went under is
   clipped away. A name or a hashtag is never cut for it. */
.vntv-live-tail {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    column-gap: 6px;
    row-gap: 1px;
    min-width: 0;
}

.vntv-live-tail > .vntv-live-links {
    flex: 0 1 auto;
}

.vntv-live-info:has(> .vntv-live-onnow) > .vntv-live-tail {
    max-height: 14px;
    overflow: clip;
}

.vntv-live-fan {
    display: flex;
    flex: 0 0 auto;
    align-items: center;
    gap: 4px;
    min-width: 0;
    max-width: 100%;
    margin-left: auto;
    color: rgba(255, 255, 255, 0.6);
    font-size: 11px;
    line-height: 14px;
    white-space: nowrap;
    text-shadow: 0 1px 2px rgba(0, 0, 0, 0.9);
}

/* Twelve pixels and its ring, the line's own height, so a one-line tail
   never clips the ring. */
.vntv-face.vntv-live-fan-face {
    flex: 0 0 auto;
    width: 12px;
    height: 12px;
    font-size: 6px;
    box-shadow: 0 0 0 1px #f2c94c;
}

.vntv-live-fan-words {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
}

.vntv-live-fan-label {
    display: none;
}

.vntv-live-fan-name {
    color: rgba(255, 255, 255, 0.9);
}

@container vntv-live (min-width: 240px) {
    .vntv-live-tail {
        flex-direction: column;
        flex-wrap: nowrap;
        align-items: stretch;
        gap: 2px;
    }

    .vntv-live-info:has(> .vntv-live-onnow) > .vntv-live-tail {
        max-height: none;
        overflow: visible;
    }

    .vntv-live-fan {
        gap: 5px;
        margin-left: 0;
        line-height: 16px;
    }

    .vntv-face.vntv-live-fan-face {
        width: 16px;
        height: 16px;
        font-size: 7px;
        box-shadow: 0 0 0 1px rgba(242, 201, 76, 0.85);
    }

    .vntv-live-fan-label {
        display: inline;
    }

    .vntv-live-fan-short {
        display: none;
    }
}

/* The streamer's profile: the fan's face where a badge goes, with the heart
   on its corner, so it reads as the same title the fan wears. A softer gold
   than the fan's own award row: it is somebody else's doing. */
.vntv-factrow.vntv-factrow-fan {
    background: var(--vntv-fill);
}

.vntv-factrow-mark.vntv-factrow-fanface {
    position: relative;
}

.vntv-factrow-fanface .vntv-face {
    width: 22px;
    height: 22px;
    font-size: 9px;
    box-shadow: 0 0 0 1.5px #f2c94c;
}

.vntv-factrow-fanpip {
    position: absolute;
    right: -3px;
    bottom: -4px;
    line-height: 0;
}

/* Trophies, Snoopers tab: Biggest fans under the boards, one line each, in
   four columns so the words and the hours line up down the list: the
   streamer, a dim "biggest fan", the fan, the hours. The columns are shares
   of the row rather than sizes of what is in it, since each row is a grid
   of its own. The streamer's is a quarter of the row, kept between 84px and
   140px, so on a wide card the words sit by the names they join and read as
   one line ("Dave, biggest fan, Bob") rather than out in the middle, and on
   a narrow one both names keep their room. */
.vntv-lb-board.vntv-lb-fans {
    margin-top: 22px;
}

.vntv-lb-row.vntv-lb-fan {
    display: grid;
    grid-template-columns: minmax(0, clamp(84px, 24%, 140px)) auto minmax(0, 1fr) 44px;
    column-gap: 12px;
}

.vntv-lb-fan-who {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    min-width: 0;
}

.vntv-face.vntv-lb-fan-face {
    width: 24px;
    height: 24px;
    font-size: 10px;
}

/* Two pixels down, so the small words stand on the line the names and the
   hours stand on rather than floating at the middle of the faces. */
.vntv-lb-fan-said {
    position: relative;
    top: 2px;
    color: var(--vntv-second);
    font-size: 11px;
    white-space: nowrap;
}

/* The fan's face as the streamer's profile shows it: in the gold ring, with
   the heart on its corner. */
.vntv-lb-fan-mark {
    position: relative;
    flex: 0 0 auto;
    line-height: 0;
}

.vntv-lb-fan-mark .vntv-face.vntv-lb-fan-face {
    box-shadow: 0 0 0 1.5px #f2c94c;
}

.vntv-lb-fan-pip {
    position: absolute;
    right: -4px;
    bottom: -4px;
    line-height: 0;
    filter: drop-shadow(0 1px 1px rgba(0, 0, 0, 0.7));
}

.vntv-lb-fan .vntv-lb-hours {
    text-align: right;
}

/* ---- 3.2.0: @names in stream titles (design/3.2.0.md 8, Victor's P7 pick)
   Mattermost's own AtMention, which draws a button round "@Name" in the
   theme's link blue, brought into the panel's look as a quiet chip. The
   button keeps none of its own furniture, as a hashtag's does not. A name
   nobody has, and one still being looked up, is the text as typed. */
.vntv .vntv-mention button.style--none {
    display: inline;
    margin: 0;
    padding: 0;
    border: 0;
    border-radius: 0;
    background: none;
    color: inherit;
    cursor: pointer;
    font: inherit;
    letter-spacing: inherit;
    line-height: inherit;
    text-align: inherit;
    text-transform: inherit;
    white-space: inherit;
    vertical-align: baseline;
    -webkit-appearance: none;
    appearance: none;
}

.vntv .vntv-mention .mention-link {
    color: inherit;
}

.vntv .vntv-mention button:focus-visible {
    outline: 2px solid var(--vntv-focus);
    outline-offset: 1px;
    border-radius: 2px;
}

/* A quiet chip, the way a person reads in a sentence elsewhere: the
   words a shade brighter than the line, on a faint lift no taller than the
   letters, which brightens when pointed at. Mattermost's button is drawn as
   a block in the line whatever its display says, so its height is set by
   its own line height, kept to the letters.

   On the panel (the line under a playing picture) the chip takes
   the skin: the panel's words on its quiet fill, the accent's quiet fill
   when pointed at. On a Live now card's words, which are drawn over its
   still, it keeps white words on a light lift on every theme, as
   everything on a picture does. */
.vntv .vntv-mention button.style--none,
.vntv .vntv-mention[data-resolved="true"] {
    padding: 0 4px;
    border-radius: 3px;
    background: var(--vntv-fill-hover);
    color: var(--vntv-fg);
    line-height: 1.3;
}

.vntv .vntv-mention button.style--none:hover {
    background: rgba(var(--vntv-accent-rgb), 0.16);
}

.vntv .vntv-live-links .vntv-mention button.style--none,
.vntv .vntv-live-links .vntv-mention[data-resolved="true"] {
    background: rgba(255, 255, 255, 0.1);
    color: var(--vntv-on-picture);
}

.vntv .vntv-live-links .vntv-mention button.style--none:hover {
    background: rgba(255, 255, 255, 0.22);
}

/* On the card's links line the chip clips its own edges, which
   cut an outline off entirely, so the keyboard's ring is drawn inside it. */
.vntv .vntv-live-links .vntv-mention button:focus-visible {
    outline: none;
    box-shadow: inset 0 0 0 2px var(--vntv-focus);
}

/* On the card's links line a name gives way before a hashtag does, and
   never below a few letters. */
.vntv-live-links .vntv-mention {
    flex: 0 1 auto;
    min-width: 3em;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
}

/* Cut short, a name ends in an ellipsis inside its own chip, rather than
   being sliced off at the edge. There the chip keeps the line's own height,
   so its words sit on the line the hashtags beside it sit on, and gives a
   pixel of its sides back. */
.vntv-live-links .vntv-mention button.style--none {
    display: inline-block;
    max-width: 100%;
    overflow: hidden;
    padding: 0 3px;
    line-height: inherit;
    text-overflow: ellipsis;
    white-space: nowrap;
    vertical-align: bottom;
}

/* A chip's own sides already part it from its neighbours, so a links line
   holding one closes its gaps to 4px: the words still stand 7px from a
   hashtag's and 10px from the next chip's, about what 6px of bare line
   gives two hashtags. */
.vntv-live-links:has(> .vntv-mention .mention-link) {
    gap: 4px;
}

/* The links line is for what can be pressed, so a name joins it only once
   Mattermost knows the person: a name nobody has, or one still being looked
   up, is plain text, and the words line above already says it. An engine
   without :has() drops this rule and shows it as text. */
.vntv-live-links .vntv-mention:not([data-inert]):not(:has(.mention-link)) {
    display: none;
}

/* So a links line holding only such names takes no room at all, rather than
   the gap above it, and the card does not shift when they are found. */
.vntv-live-links:not(:has(> .vntv-link, > .vntv-twitch-out, > .vntv-mention[data-inert], > .vntv-mention .mention-link)) {
    display: none;
}

/* ---- 3.2.0: a shared plan in a channel ----
   The card a shared plan's post is drawn as (PlanPostCard.tsx, design/3.2.0.md
   6.5), on the clip card's pattern: Mattermost's theme's colours, so it reads
   on Denim's white and Onyx's black alike, with the accent only on the
   eyebrow's calendar and red only on LIVE. The banner: the title's
   art at 2:1 across the top, with LIVE on it, and the words under it; a plan
   with no art of its own is words only.

   The card is a size container, so a narrow card (a thread in the side
   panel) can lay itself out differently; a container query cannot style the
   container itself, so what it draws sits in .vntv-plancard-in, one level
   in, and that is what the faces arrange. */
.vntv.vntv-plancard {
    width: 100%;
    max-width: 480px;
    height: auto;
    margin: 2px 0;
    padding: 8px;
    border: 1px solid rgba(var(--center-channel-color-rgb, 63, 67, 80), 0.16);
    border-radius: 8px;
    overflow: hidden;
    background: rgba(var(--center-channel-color-rgb, 63, 67, 80), 0.04);
    color: var(--center-channel-color, #3f4350);
    font-size: 13px;
    line-height: 1.35;
    text-align: left;
    container-type: inline-size;
}

.vntv-plancard-in {
    display: flex;
    flex-direction: row;
    align-items: stretch;
    gap: 12px;
    min-width: 0;
}

/* As on the clip card: inside a permalink preview nothing inside a button,
   and no svg, takes the pointer. */
.vntv-plancard button *,
.vntv-plancard svg {
    pointer-events: none;
}

.vntv-plancard-art {
    position: relative;
    display: block;
    flex: 0 0 auto;
    margin: 0;
    border: 0;
    padding: 0;
    overflow: hidden;
    background: #000000;
    cursor: default;
}

.vntv-plancard[data-state="live"] .vntv-plancard-art {
    cursor: pointer;
}

.vntv-plancard-img {
    position: absolute;
    inset: 0;
    display: block;
    width: 100%;
    height: 100%;
    object-fit: cover;
}

/* LIVE: the plugin red, on the picture's corner, or after the eyebrow on a
   card with no picture. The theme's LIVE red, as every LIVE tag. */
.vntv-plancard-live {
    display: inline-flex;
    flex: 0 0 auto;
    align-items: center;
    gap: 4px;
    height: 16px;
    padding: 0 6px;
    border-radius: 3px;
    background: var(--vntv-live);
    color: #ffffff;
    font-family: var(--vntv-display);
    font-size: 10px;
    font-weight: 600;
    letter-spacing: 0.08em;
    line-height: 16px;
    text-transform: uppercase;
}

.vntv-plancard-live::before {
    content: '';
    width: 5px;
    height: 5px;
    border-radius: 50%;
    background: #ffffff;
}

.vntv-plancard-art .vntv-plancard-live {
    position: absolute;
    top: 6px;
    left: 6px;
}

.vntv-plancard-live-inline {
    margin-left: 4px;
}

/* The eyebrow's LIVE is for a card whose picture cannot carry it. */
.vntv-plancard[data-art="true"] .vntv-plancard-live-inline {
    display: none;
}

.vntv-plancard-body {
    display: flex;
    flex: 1 1 200px;
    flex-direction: column;
    justify-content: center;
    gap: 3px;
    min-width: 0;
}

.vntv-plancard-eyebrow {
    display: flex;
    align-items: center;
    gap: 5px;
    margin: 0;
    color: rgba(var(--center-channel-color-rgb, 63, 67, 80), 0.75);
    font-family: var(--vntv-display);
    font-size: 10px;
    font-weight: 600;
    letter-spacing: 0.1em;
    line-height: 13px;
    text-transform: uppercase;
}

/* The eyebrow's calendar in the theme's accent. */
.vntv-plancard-icon {
    display: inline-flex;
    flex: 0 0 auto;
    color: var(--vntv-accent);
    font-style: normal;
}

.vntv-plancard-title {
    display: flex;
    align-items: center;
    gap: 6px;
    min-width: 0;
    margin: 0;
    font-size: 14px;
    font-weight: 600;
    line-height: 18px;
}

.vntv-plancard-kind {
    display: inline-flex;
    flex: 0 0 auto;
    color: rgba(var(--center-channel-color-rgb, 63, 67, 80), 0.64);
}

.vntv-plancard-name {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
}

/* "A plan in" and the server's name, before the first snapshot: the title's place, quieter. */
.vntv-plancard-waiting {
    margin: 0;
    color: rgba(var(--center-channel-color-rgb, 63, 67, 80), 0.75);
    font-size: 14px;
    font-weight: 600;
    line-height: 18px;
}

/* The host's face with the first three who are in tucked behind it, and
   the names: "Dave with Bob and Erin". */
/* "This plan was cancelled.", "This stream has been and gone.": dimmed, as
   6.5 has it, but still about 4.8:1 on Denim's white. */
.vntv-plancard-gone {
    margin: 0;
    color: rgba(var(--center-channel-color-rgb, 63, 67, 80), 0.75);
    font-size: 13px;
    line-height: 18px;
}

.vntv-plancard[data-dimmed="true"] .vntv-plancard-icon {
    color: inherit;
}

.vntv-plancard-who {
    display: flex;
    align-items: center;
    gap: 6px;
    min-width: 0;
    margin: 1px 0 0;
    color: rgba(var(--center-channel-color-rgb, 63, 67, 80), 0.75);
    font-size: 12px;
    line-height: 18px;
}

.vntv-plancard-who b {
    color: var(--center-channel-color, #3f4350);
    font-weight: 600;
}

.vntv-plancard-faces {
    display: inline-flex;
    flex: 0 0 auto;
}

.vntv-face.vntv-plancard-face {
    position: relative;
    width: 18px;
    height: 18px;
    font-size: 8px;
    box-shadow: 0 0 0 1.5px var(--center-channel-bg, #ffffff);
}

.vntv-plancard-face + .vntv-plancard-face {
    margin-left: -5px;
}

/* The host's face in front, the others tucked in behind it in order. */
.vntv-plancard-face:nth-child(1) {
    z-index: 3;
}

.vntv-plancard-face:nth-child(2) {
    z-index: 2;
}

.vntv-plancard-face:nth-child(3) {
    z-index: 1;
}

.vntv-plancard-names {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
}

.vntv-plancard-when {
    margin: 0;
    color: rgba(var(--center-channel-color-rgb, 63, 67, 80), 0.75);
    font-size: 12px;
    line-height: 16px;
    font-variant-numeric: tabular-nums;
}

.vntv-plancard-lede {
    color: var(--center-channel-color, #3f4350);
    font-weight: 600;
}

.vntv-plancard-actions {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 6px 8px;
    margin-top: 4px;
}

/* Join: outlined in the theme's button colour beside Watch, and filled when
   it is the card's one button. In ✓ is pressed; Full is greyed. */
/* Stock's tertiary button, the same second-rank shape as the panel's,
   24px in the card's dense row. */
.vntv-plancard-join {
    display: inline-flex;
    flex: 0 0 auto;
    align-items: center;
    height: 24px;
    margin: 0;
    border: 1px solid transparent;
    border-radius: 4px;
    padding: 0 10px;
    background: rgba(var(--button-bg-rgb, 28, 88, 217), 0.08);
    color: var(--button-bg, #1c58d9);
    cursor: pointer;
    font-size: 12px;
    font-weight: 600;
    line-height: 1;
}

.vntv-plancard-join:hover {
    background: rgba(var(--button-bg-rgb, 28, 88, 217), 0.16);
}

.vntv-plancard-join[data-primary="true"] {
    border-color: var(--button-bg, #1c58d9);
    background: var(--button-bg, #1c58d9);
    color: var(--button-color, #ffffff);
}

.vntv-plancard-join[data-primary="true"]:hover {
    background: rgba(var(--button-bg-rgb, 28, 88, 217), 0.88);
}

.vntv-plancard-join[aria-pressed="true"] {
    border-color: var(--button-bg, #1c58d9);
    background: rgba(var(--button-bg-rgb, 28, 88, 217), 0.12);
}

/* Full is the only thing saying why there is no joining, so it is
   read: the second-rank words on a quiet line, not a faded accent. */
.vntv-plancard-join[aria-disabled="true"] {
    border-color: rgba(var(--center-channel-color-rgb, 63, 67, 80), 0.16);
    background: transparent;
    color: rgba(var(--center-channel-color-rgb, 63, 67, 80), 0.64);
    cursor: default;
}

.vntv-plancard-join:focus-visible {
    outline: none;
    box-shadow: 0 0 0 2px var(--center-channel-bg, #ffffff), 0 0 0 4px var(--button-bg, #1c58d9);
}

.vntv-plancard-mine {
    color: rgba(var(--center-channel-color-rgb, 63, 67, 80), 0.75);
    font-size: 12px;
    font-weight: 600;
}

/* The answer to a Join that did not go: always in the card, so it is
   announced, and taking no room while it says nothing. */
.vntv-plancard-body > .vntv-plancard-answer:empty {
    margin-top: -3px;
}

/* The banner, edge to edge over the words, twice as wide as it is tall;
   only for a plan it can show. 2:1 is a Steam header's shape (460 by 215)
   and takes a little off the top and bottom of a 16:9 backdrop, so the art
   reads whole, its name included. */
.vntv-plancard {
    padding: 0;
}

.vntv-plancard .vntv-plancard-in {
    flex-direction: column;
    gap: 0;
}

.vntv-plancard .vntv-plancard-art {
    width: 100%;
    aspect-ratio: 2 / 1;
}

.vntv-plancard .vntv-plancard-body {
    flex: 0 0 auto;
    padding: 10px 12px 12px;
}

/* A card under 360px (a thread in the side panel): the buttons share the
   width (6.5, Narrow), and the banner keeps its 2:1. */
@container (max-width: 359px) {
    .vntv-plancard-actions > button {
        flex: 1 1 0;
        justify-content: center;
    }
}

/* ---- 3.2.0: the thumb on a shared clip's card ----
   design/3.2.0.md 9.3, Victor's P6 pick: a pill after Watch, the panel's
   upvote in the theme's colours. Pressed, it takes the theme's button colour
   and fills the hand, as Mattermost marks a reaction of your own; a count
   somebody may not change (let in, or their own clip) is plain and dashed. */
.vntv-postclip-up {
    display: inline-flex;
    flex: 0 0 auto;
    align-items: center;
    gap: 5px;
    height: 24px;
    margin: 0;
    padding: 0 9px;
    border: 1px solid rgba(var(--center-channel-color-rgb, 63, 67, 80), 0.24);
    border-radius: 999px;
    background: transparent;
    color: rgba(var(--center-channel-color-rgb, 63, 67, 80), 0.88);
    font-size: 12px;
    font-weight: 600;
    font-variant-numeric: tabular-nums;
    line-height: 1;
    white-space: nowrap;
}

button.vntv-postclip-up {
    cursor: pointer;
    transition: background-color 120ms ease, border-color 120ms ease, color 120ms ease;
}

button.vntv-postclip-up:hover {
    border-color: var(--button-bg, #1c58d9);
    color: var(--button-bg, #1c58d9);
}

.vntv-postclip-up[aria-pressed="true"] {
    border-color: var(--button-bg, #1c58d9);
    background: rgba(var(--button-bg-rgb, 28, 88, 217), 0.12);
    color: var(--button-bg, #1c58d9);
}

.vntv-postclip-up[aria-pressed="true"] svg {
    fill: currentColor;
}

.vntv-postclip-up:focus-visible {
    outline: none;
    box-shadow: 0 0 0 2px var(--center-channel-bg, #ffffff), 0 0 0 4px var(--button-bg, #1c58d9);
}

/* A count somebody may not change: plain, at the meta line's 0.75 (about
   4.7:1 on Denim), the pill dashed. A let-in reader's old vote is still
   shown (9.2), by the filled hand alone. */
.vntv-postclip-up-static {
    color: rgba(var(--center-channel-color-rgb, 63, 67, 80), 0.75);
    cursor: default;
}

.vntv-postclip-up-static[data-voted="true"] svg {
    fill: currentColor;
}

.vntv-postclip-up.vntv-postclip-up-static {
    border-style: dashed;
}

/* ---- 3.2.0: the soundboard ----
   design/3.2.0.md 7.13, as Victor picked it from the P1 preview: a sixth tool
   in the footer's bar, shown only while the reader is in voice, and its sheet,
   which rises from the bar like every other sheet, with A tiles: the emoji
   over the name inside. The tiles are stock cards on the sheet's
   ground (a faint fill, a 1px line, radius 4px), so they take the theme;
   your sound is the accent, as progress is. The add flow takes the grid's
   place in the same sheet. */



/* The first time ever in a browser, a small dot in the accent until it is
   opened, ringed in the ground it sits on. */
.vntv-snd-dot {
    position: absolute;
    top: 3px;
    right: 3px;
    box-sizing: border-box;
    width: 8px;
    height: 8px;
    border: 2px solid var(--vntv-bg);
    border-radius: 50%;
    background: var(--vntv-accent);
}

/* On the bar, the accent's dot ringed in the bar's ground, as the
   header's new-clips dot is. */
.vntv-bar-sounds .vntv-snd-dot {
    top: 5px;
    right: 5px;
    width: 9px;
    height: 9px;
    border-color: var(--vntv-bg);
    background: var(--vntv-accent);
}

/* The sheet: scrolls inside itself past about 60 % of the panel. The
   scrollbar's room is kept whether it scrolls or not, and taken from the
   right padding (the sheet measures it), so the grid keeps its width and its
   four tiles a row at 304 px when a reason box makes the sheet scroll. */
.vntv-sheet-sounds {
    max-height: min(600px, 64vh);
    overflow-y: auto;
    overscroll-behavior: contain;
    scrollbar-gutter: stable;
    scrollbar-width: thin;
    scrollbar-color: rgba(var(--vntv-fg-rgb), 0.24) transparent;
    padding-right: calc(14px - var(--vntv-snd-gutter, 0px));
}

.vntv-snd-add {
    display: flex;
    flex-direction: column;
    gap: 12px;
    min-width: 0;
}

/* Why the tiles are off: a quiet line under the head, with its reason. */
.vntv-snd-why {
    display: flex;
    align-items: flex-start;
    gap: 9px;
    margin: 0;
    padding: 9px 11px;
    border: 1px solid var(--vntv-line);
    border-radius: 4px;
    background: var(--vntv-fill);
    color: var(--vntv-second);
    font-size: 12px;
    line-height: 1.45;
}

.vntv-snd-why-icon {
    display: inline-flex;
    flex: 0 0 auto;
    margin-top: 2px;
    color: var(--vntv-icon);
}

.vntv-snd-why-icon svg {
    display: block;
}

/* Somebody else's sound: a thin bar in the accent under the head, emptying,
   as stock's progress is drawn. It keeps
   its place while hidden, so the sheet is the same height in every state
   and its top edge never moves when a sound starts or a wait ends. */
.vntv-snd-busy {
    height: 2px;
    margin: -6px 0 -4px;
    overflow: hidden;
    border-radius: 1px;
    background: var(--vntv-fill-hover);
}

.vntv-snd-busy[data-kind="none"] {
    visibility: hidden;
}

.vntv-snd-busy span {
    display: block;
    height: 100%;
    background: var(--vntv-accent);
    transform-origin: left center;
}

.vntv-snd-busy[data-kind="cooldown"] span {
    background: rgba(var(--vntv-fg-rgb), 0.48);
}

/* ---- The grid, and a sound in it ----
   Tiles at least 56 px wide, as many to a row as fit: four at a 304 px
   panel, five at 400, six at 464 (design/3.2.0.md 7.13, V32), and on a wide
   panel never much over 70 px, since another column comes first. */
.vntv-snd-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(56px, 1fr));
    gap: 8px;
    min-width: 0;
}

.vntv-snd-cell {
    position: relative;
    min-width: 0;
}

.vntv-snd-tile {
    position: relative;
    display: block;
    box-sizing: border-box;
    width: 100%;
    min-width: 0;
    margin: 0;
    padding: 0;
    border: none;
    background: transparent;
    color: inherit;
    cursor: pointer;
    font: inherit;
    text-align: center;
    -webkit-user-select: none;
    user-select: none;
}

.vntv-snd-tile[aria-disabled="true"] {
    cursor: default;
}

.vntv-snd-tile:focus-visible {
    outline: none;
}

.vntv-snd-face {
    position: relative;
    display: flex;
    box-sizing: border-box;
    min-width: 0;
    border: 1px solid var(--vntv-line);
    background: var(--vntv-fill);
    transition: transform 90ms ease, border-color 150ms ease, background-color 150ms ease, box-shadow 200ms ease;
}

/* The stock ring, round the tile's own edge. */
.vntv-snd-tile:focus-visible .vntv-snd-face {
    outline: 2px solid var(--vntv-focus);
    outline-offset: 0;
}

.vntv-snd-tile:active:not([aria-disabled="true"]) .vntv-snd-face {
    transform: scale(0.96);
}

.vntv-snd-emoji {
    display: block;
    flex: 0 0 auto;
    width: 30px;
    height: 30px;
    object-fit: contain;
    pointer-events: none;
}

.vntv-snd-emoji-gone {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    color: var(--vntv-icon);
}

/* A name runs to two lines, so the 16 characters a name may have show whole
   on the board: words wrap at their spaces, and only a single word too long
   for a line is broken (one of the P1 preview's proposals, approved with
   Victor's pick). */
.vntv-snd-name {
    display: -webkit-box;
    max-width: 100%;
    overflow: hidden;
    color: var(--vntv-fg);
    font-family: var(--vntv-display);
    font-size: 10px;
    font-weight: 600;
    letter-spacing: 0.06em;
    line-height: 12px;
    overflow-wrap: anywhere;
    text-transform: uppercase;
    -webkit-box-orient: vertical;
    -webkit-line-clamp: 2;
}

/* The tile: a square'ish face, the emoji over the name inside it. The emoji
   sits at the same height in every tile, the name under it on one line or
   two; every tile is as tall as two lines need, about 74 px. A fixed height
   rather than a square: an aspect ratio would pass that height on as a
   width, and on a narrow sheet the tiles would run into each other. */
.vntv-snd-grid .vntv-snd-face {
    flex-direction: column;
    align-items: center;
    justify-content: flex-start;
    gap: 4px;
    height: 74px;
    padding: 9px 2px 4px;
    border-radius: 4px;
}

.vntv-snd-grid .vntv-snd-name {
    flex: 0 0 auto;
    letter-spacing: 0.02em;
    text-align: center;
}

/* The add tile: a dashed outline, a plus, ADD. */
.vntv-snd-addtile .vntv-snd-face {
    border: 1px dashed var(--vntv-line-hover);
    background: transparent;
    box-shadow: none;
    color: var(--vntv-icon);
}

.vntv-snd-addtile .vntv-snd-name {
    color: var(--vntv-second);
}

.vntv-snd-plus {
    display: inline-flex;
    flex: 0 0 auto;
    align-items: center;
    justify-content: center;
    height: 30px;
}

.vntv-snd-plus svg {
    display: block;
}

/* ---- Your sound playing: a ring in the accent runs once round the tile's
   edge over the sound's length. ---- */
.vntv-snd-ring {
    position: absolute;
    inset: -1px;
    width: calc(100% + 2px);
    height: calc(100% + 2px);
    overflow: visible;
    pointer-events: none;
}

/* The rect is in a 100-unit box stretched over the tile: 6 units is about
   the face's 4px corner. */
.vntv-snd-ring rect {
    fill: none;
    stroke: var(--vntv-accent);
    stroke-width: 3;
    stroke-linecap: round;
    rx: 6px;
}

/* No glow (stock has none): the line and a faint fill of the accent. */
.vntv-snd-tile[data-playing="true"] .vntv-snd-face {
    border-color: rgba(var(--vntv-accent-rgb), 0.32);
    background: rgba(var(--vntv-accent-rgb), 0.08);
}

/* While your sound plays and through the wait after it, the others step back
   and only yours stands out. */
.vntv-snd-grid[data-phase="playing"] .vntv-snd-cell:not([data-active="true"], [data-add="true"]),
.vntv-snd-grid[data-phase="cooldown"] .vntv-snd-cell:not([data-active="true"], [data-add="true"]) {
    opacity: 0.45;
}

/* ---- The wait after your sound: a sweep receding clockwise over the one
   you pressed, as a cooldown does in a game, with the seconds left in its
   corner, and the grey bar under the head. The sweep is the
   sheet's own ground laid over the tile, so it dims it on every theme. ---- */
.vntv-snd-sweep {
    position: absolute;
    inset: -1px;
    border-radius: inherit;
    background: conic-gradient(transparent calc((1 - var(--vntv-snd-left)) * 360deg), rgba(var(--vntv-bg-rgb), 0.72) 0);
    pointer-events: none;
}

.vntv-snd-count {
    position: absolute;
    top: 4px;
    right: 4px;
    min-width: 17px;
    height: 17px;
    padding: 0 4px;
    box-sizing: border-box;
    border-radius: 9px;
    background: var(--vntv-fg);
    color: var(--vntv-bg);
    font-family: var(--vntv-display);
    font-size: 10.5px;
    font-variant-numeric: tabular-nums;
    font-weight: 600;
    line-height: 17px;
    text-align: center;
}

/* Somebody else's sound: every face inert until it ends; muted, deafened,
   not allowed, or Mumble out of reach: grey and inert. The add tile, where
   it is offered, stays as it is, and so do your own sounds' dots: deleting
   one is a takeback, open whatever Mumble says. Not by fading the
   whole tile, which left the names at 1.8:1 on a light theme and 3:1 on a
   dark one: the art fades and loses its colour, the name takes the
   disabled words (0.56, about 3:1 on Denim and more on the dark themes)
   and the tile no fill. */
.vntv-snd-grid[data-phase="busy"] .vntv-snd-cell:not([data-add="true"]) .vntv-snd-face,
.vntv-snd-grid[data-phase="off"] .vntv-snd-cell:not([data-add="true"]) .vntv-snd-face {
    background: transparent;
}

.vntv-snd-grid[data-phase="busy"] .vntv-snd-cell:not([data-add="true"]) .vntv-snd-face > :not(.vntv-snd-name),
.vntv-snd-grid[data-phase="off"] .vntv-snd-cell:not([data-add="true"]) .vntv-snd-face > :not(.vntv-snd-name) {
    opacity: 0.45;
    filter: grayscale(1);
}

.vntv-snd-grid[data-phase="busy"] .vntv-snd-cell:not([data-add="true"]) .vntv-snd-name,
.vntv-snd-grid[data-phase="off"] .vntv-snd-cell:not([data-add="true"]) .vntv-snd-name {
    color: rgba(var(--vntv-fg-rgb), 0.56);
}

/* Just added: the new one pulses once. */
.vntv-snd-tile[data-fresh="true"] .vntv-snd-face {
    animation: vntv-snd-fresh 1.8s ease-out 1;
}

@keyframes vntv-snd-fresh {
    0% { border-color: var(--vntv-accent); box-shadow: 0 0 0 0 rgba(var(--vntv-accent-rgb), 0.75); }
    60% { border-color: rgba(var(--vntv-accent-rgb), 0.5); box-shadow: 0 0 0 9px rgba(var(--vntv-accent-rgb), 0); }
    100% { border-color: var(--vntv-line); box-shadow: 0 0 0 0 rgba(var(--vntv-accent-rgb), 0); }
}

/* Your own sounds' three dots, when pointed at or focused, always on touch:
   a small pill tucked into the corner, clear of the emoji. On the
   sheet's ground with its line, the glyph an icon's grey. */
.vntv-snd-more {
    position: absolute;
    top: 3px;
    right: 3px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 22px;
    height: 14px;
    padding: 0;
    border: none;
    border-radius: 4px;
    background: var(--vntv-bg);
    box-shadow: inset 0 0 0 1px var(--vntv-line);
    color: var(--vntv-icon);
    cursor: pointer;
    opacity: 0;
    transition: opacity 120ms ease;
}

.vntv-snd-more svg {
    display: block;
}

.vntv-snd-cell:focus-within .vntv-snd-more,
.vntv-snd-cell[data-menu="true"] .vntv-snd-more {
    opacity: 1;
}

.vntv-snd-cell[data-menu="true"] .vntv-snd-face {
    border-color: var(--vntv-line-focus);
}

/* One of your own sounds' choices: a row of its own in the grid, after the
   row that holds the sound, and in it a short list under a notch pointing up
   at the sound. The rows under it move down; nothing is covered. Stock's
   menu: the ground, a 1px line, radius 4px, a raised shadow. */
.vntv-snd-menu {
    grid-column: 1 / -1;
    min-width: 0;
}

.vntv-snd-menu-box {
    position: relative;
    display: flex;
    flex-direction: column;
    box-sizing: border-box;
    width: min(100%, var(--vntv-snd-menu-width));
    margin-left: var(--vntv-snd-menu-left);
    padding: 4px;
    border: 1px solid var(--vntv-line);
    border-radius: 4px;
    background: var(--vntv-bg);
    box-shadow: var(--vntv-raised);
}

/* The notch, in the gap between the sound's row and the list. */
.vntv-snd-menu-box::before {
    content: '';
    position: absolute;
    top: -5px;
    left: calc(var(--vntv-snd-notch) - 5px);
    width: 8px;
    height: 8px;
    border-top: 1px solid var(--vntv-line);
    border-left: 1px solid var(--vntv-line);
    background: var(--vntv-bg);
    transform: rotate(45deg);
}

.vntv-snd-menu-item {
    display: flex;
    align-items: center;
    gap: 10px;
    height: 30px;
    margin: 0;
    padding: 0 10px;
    border: none;
    border-radius: 4px;
    background: transparent;
    color: var(--vntv-fg);
    cursor: pointer;
    font: inherit;
    font-size: 12.5px;
    text-align: left;
    white-space: nowrap;
}

.vntv-snd-menu-item svg {
    display: block;
    flex: 0 0 auto;
    color: var(--vntv-icon);
}

.vntv-snd-menu-item[data-danger="true"],
.vntv-snd-menu-item[data-danger="true"] svg {
    color: var(--vntv-danger);
}

.vntv-snd-menu-item[data-armed="true"] {
    background: rgba(var(--vntv-danger-rgb), 0.08);
}

.vntv-snd-menu-item:focus-visible {
    outline: 2px solid var(--vntv-focus);
    outline-offset: -2px;
}

/* Under the grid: what just happened, and Delete all my sounds. */
.vntv-snd-foot {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 10px;
    min-width: 0;
}

.vntv-snd-said {
    min-width: 0;
    color: var(--vntv-second);
    font-size: 11.5px;
    line-height: 1.4;
}

/* Delete all my sounds is a destructive action: stock's danger
   tertiary button, 32px, at rest, its filled danger button armed, as the
   profile popover's Delete my sounds. */
.vntv-snd-quiet {
    flex: 0 0 auto;
    min-height: 32px;
    margin: 0 0 0 auto;
    padding: 0 16px;
    border: none;
    border-radius: 4px;
    background: rgba(var(--vntv-danger-rgb), 0.08);
    color: var(--vntv-danger);
    cursor: pointer;
    font: inherit;
    font-size: 12px;
    font-weight: 600;
    transition: background-color 120ms ease;
}

.vntv-snd-quiet[data-armed="true"] {
    background: var(--vntv-danger-fill);
    color: var(--vntv-on-danger);
}

.vntv-snd-quiet:focus-visible,
.vntv-snd-more:focus-visible,
.vntv-snd-source:focus-visible,
.vntv-snd-clip:focus-visible,
.vntv-snd-pick:focus-visible,
.vntv-snd-emoji-button:focus-visible,
.vntv-snd-rec-button:focus-visible {
    outline: 2px solid var(--vntv-focus);
    outline-offset: 1px;
}

/* Inside a list that scrolls, whose edge would cut a ring drawn outside:
   the ring drawn inside instead, as stock does where a shadow is clipped. */
.vntv-snd-clip:focus-visible,
.vntv-snd-pick:focus-visible {
    outline-offset: -2px;
}

/* ---- Adding a sound ---- */
/* The add flow on the sheet's ground: rows and fields are stock
   cards and inputs (a 1px line, radius 4px), words the panel's own, the
   accent for what is chosen and for progress, LIVE's red while the
   microphone records, and the theme's error words for a refusal. */

/* The first-run invitation, on an empty board. */
.vntv-snd-invite {
    display: flex;
    flex-direction: column;
    gap: 10px;
}

.vntv-snd-invite-words {
    margin: 0;
    color: var(--vntv-second);
    font-size: 12.5px;
    line-height: 1.45;
}

/* The three ways in, as rows. */
.vntv-snd-sources {
    display: flex;
    flex-direction: column;
    gap: 6px;
}

.vntv-snd-source {
    display: flex;
    align-items: center;
    gap: 12px;
    width: 100%;
    min-width: 0;
    margin: 0;
    padding: 10px 12px 10px 10px;
    border: 1px solid var(--vntv-line);
    border-radius: 4px;
    background: transparent;
    color: inherit;
    cursor: pointer;
    font: inherit;
    text-align: left;
    transition: border-color 150ms ease, background-color 150ms ease;
}

.vntv-snd-source-icon {
    display: inline-flex;
    flex: 0 0 auto;
    align-items: center;
    justify-content: center;
    width: 34px;
    height: 34px;
    border-radius: 50%;
    background: rgba(var(--vntv-accent-rgb), 0.08);
    color: var(--vntv-accent);
}

.vntv-snd-source-icon svg {
    display: block;
}

.vntv-snd-source-words {
    display: flex;
    flex: 1 1 auto;
    flex-direction: column;
    gap: 2px;
    min-width: 0;
}

.vntv-snd-source-title {
    color: var(--vntv-fg);
    font-family: var(--vntv-display);
    font-size: 12px;
    font-weight: 600;
    letter-spacing: 0.08em;
    line-height: 15px;
    text-transform: uppercase;
}

.vntv-snd-source-line {
    color: var(--vntv-second);
    font-size: 11.5px;
    line-height: 15px;
}

/* Recording: the warning in plain words, then one big button. */
.vntv-snd-record {
    display: flex;
    flex-direction: column;
    gap: 14px;
}

.vntv-snd-warn {
    display: flex;
    align-items: flex-start;
    gap: 9px;
    margin: 0;
    padding: 10px 12px;
    border: 1px solid var(--vntv-line);
    border-left: 3px solid var(--vntv-accent);
    border-radius: 4px;
    background: var(--vntv-fill);
    color: var(--vntv-fg);
    font-size: 12px;
    line-height: 1.5;
}

.vntv-snd-warn-refused {
    border-color: rgba(var(--vntv-danger-rgb), 0.32);
    border-left-color: var(--vntv-danger);
    background: rgba(var(--vntv-danger-rgb), 0.08);
    color: var(--vntv-danger);
}

.vntv-snd-warn-icon {
    display: inline-flex;
    flex: 0 0 auto;
    margin-top: 2px;
    color: var(--vntv-accent);
}

.vntv-snd-warn-refused .vntv-snd-warn-icon {
    color: var(--vntv-danger);
}

.vntv-snd-warn-icon svg {
    display: block;
}

.vntv-snd-rec {
    display: flex;
    align-items: center;
    gap: 16px;
    padding: 2px 4px;
}

.vntv-snd-rec-button {
    position: relative;
    display: inline-flex;
    flex: 0 0 auto;
    align-items: center;
    justify-content: center;
    width: 58px;
    height: 58px;
    margin: 4px;
    padding: 0;
    border: none;
    border-radius: 50%;
    background: var(--vntv-live);
    color: #ffffff;
    cursor: pointer;
    box-shadow: var(--elevation-1, 0 2px 3px 0 rgba(0, 0, 0, 0.08));
}

.vntv-snd-rec-button svg {
    display: block;
}

.vntv-snd-rec-button[data-live="true"] {
    background: rgba(var(--vntv-live-rgb), 0.08);
    color: var(--vntv-live);
    box-shadow: none;
}

/* The keyboard's ring stands clear of the progress ring while it
   records, so the two never lie on each other. */
.vntv-snd-rec-button[data-live="true"]:focus-visible {
    outline-offset: 7px;
}

.vntv-snd-rec-ring {
    position: absolute;
    inset: -5px;
    width: calc(100% + 10px);
    height: calc(100% + 10px);
    transform: rotate(-90deg);
    pointer-events: none;
}

.vntv-snd-rec-ring circle {
    fill: none;
    stroke: var(--vntv-live);
    stroke-width: 3.5;
    stroke-linecap: round;
}

.vntv-snd-rec-side {
    display: flex;
    flex-direction: column;
    gap: 3px;
    min-width: 0;
}

.vntv-snd-rec-title {
    color: var(--vntv-fg);
    font-family: var(--vntv-display);
    font-size: 14px;
    font-weight: 600;
    letter-spacing: 0.1em;
    text-transform: uppercase;
}

.vntv-snd-rec-time {
    color: var(--vntv-second);
    font-size: 12px;
    font-variant-numeric: tabular-nums;
}

/* The sound so far, growing left to right over six seconds. */
.vntv-snd-meter {
    position: relative;
    height: 44px;
    overflow: hidden;
    border-radius: 4px;
    background: repeating-linear-gradient(90deg, var(--vntv-fill-hover) 0, var(--vntv-fill-hover) 10px, var(--vntv-fill) 10px, var(--vntv-fill) 12px);
}

.vntv-snd-wave {
    position: absolute;
    top: 6px;
    bottom: 6px;
    left: 0;
    display: flex;
    align-items: center;
    gap: 1px;
}

.vntv-snd-meter .vntv-snd-wave {
    padding: 0 0 0 4px;
}

.vntv-snd-wave i {
    display: block;
    flex: 1 1 0;
    min-width: 1px;
    border-radius: 1px;
    background: var(--vntv-icon);
}

.vntv-snd-meter .vntv-snd-wave i {
    background: var(--vntv-live);
}

/* The trim: the clip trimmer's track, with the sound's shape on it in place
   of the stripes, and a little taller. Not a film strip, so the
   track is a faint fill of the panel's words and what is cut away is
   dimmed with the panel's own ground. */
.vntv-snd-trim .vntv-trim-track {
    height: 56px;
}

.vntv-snd-trim .vntv-trim-track::before {
    background: var(--vntv-fill-hover);
}

.vntv-snd-trim .vntv-trim-dim {
    background: rgba(var(--vntv-bg-rgb), 0.6);
}

.vntv-snd-trim .vntv-trim-thumb {
    height: 56px;
}

.vntv-snd-trim .vntv-snd-wave {
    top: 13px;
    right: 0;
    bottom: 13px;
}

.vntv-snd-readout {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
    margin: 4px 0 0;
}

.vntv-snd-readout .vntv-trim-readout {
    flex: 1 1 auto;
    color: var(--vntv-fg);
    font-size: 15px;
}

.vntv-snd-listen {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    height: 32px;
}

.vntv-snd-listen svg {
    display: block;
}

.vntv-snd-from {
    flex: 0 0 auto;
    min-width: 64px;
    color: var(--vntv-second);
    font-size: 11px;
    font-variant-numeric: tabular-nums;
    text-align: right;
}

.vntv-snd-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
}

.vntv-snd-hint {
    margin: 0;
    color: var(--vntv-second);
    font-size: 11.5px;
    line-height: 1.4;
}

/* A long sound's whole length over its trim: the window the trim shows,
   lit, and the selection in the accent. A press moves both there. */
.vntv-snd-overview {
    position: relative;
    height: 10px;
    margin: 0 22px 4px;
    border-radius: 3px;
    background: var(--vntv-fill-hover);
    cursor: pointer;
}

.vntv-snd-overview-view {
    position: absolute;
    top: 0;
    bottom: 0;
    border-radius: 3px;
    background: rgba(var(--vntv-fg-rgb), 0.12);
    box-shadow: inset 0 0 0 1px var(--vntv-line-focus);
}

.vntv-snd-overview-sel {
    position: absolute;
    top: 2px;
    bottom: 2px;
    min-width: 2px;
    border-radius: 2px;
    background: var(--vntv-accent);
}

/* A list that scrolls inside the sheet (the clips, the emoji): a fade at its
   foot while there is more under it. */
.vntv-snd-scroll {
    overflow-y: auto;
    overscroll-behavior: contain;
    scrollbar-width: thin;
    scrollbar-color: rgba(var(--vntv-fg-rgb), 0.24) transparent;
}

.vntv-snd-scroll[data-more="true"] {
    -webkit-mask-image: linear-gradient(180deg, #000000 calc(100% - 34px), transparent);
    mask-image: linear-gradient(180deg, #000000 calc(100% - 34px), transparent);
}

/* Every clip the reader may watch, newest first, drawn as the Clips sheet
   draws one; a search over them past six. */
.vntv-snd-clip-pick {
    display: flex;
    flex-direction: column;
    gap: 8px;
    min-width: 0;
}

.vntv-snd-clip-pick .vntv-input {
    width: 100%;
}

.vntv-snd-clips-scroll {
    max-height: 318px;
}

.vntv-snd-clips {
    display: flex;
    flex-direction: column;
    gap: 6px;
    margin: 0;
    padding: 0;
    list-style: none;
}

.vntv-snd-clip {
    display: flex;
    align-items: center;
    gap: 10px;
    width: 100%;
    min-width: 0;
    margin: 0;
    padding: 6px;
    border: 1px solid var(--vntv-line);
    border-radius: 4px;
    background: transparent;
    color: inherit;
    cursor: pointer;
    font: inherit;
    text-align: left;
}

.vntv-snd-clip-thumb {
    position: relative;
    flex: 0 0 auto;
    width: 80px;
    height: 45px;
    overflow: hidden;
    border-radius: 4px;
    background: #0b0b0c;
}

/* The clip being cut from, over the trim: the same line, not a button. */
.vntv-snd-clip-chosen {
    cursor: default;
}

.vntv-snd-clip-chosen .vntv-snd-clip-thumb {
    width: 64px;
    height: 36px;
}

.vntv-snd-clip-len {
    position: absolute;
    z-index: 1;
    right: 3px;
    bottom: 3px;
    padding: 0 4px;
    border-radius: 3px;
    background: rgba(0, 0, 0, 0.78);
    color: #ffffff;
    font-family: var(--vntv-display);
    font-size: 10px;
    font-variant-numeric: tabular-nums;
    line-height: 14px;
}

.vntv-snd-clip-words {
    display: flex;
    flex: 1 1 auto;
    flex-direction: column;
    gap: 2px;
    min-width: 0;

    /* No width of its own to give the sheet, so a long title is
       cut short with its ellipsis instead of widening the sheet past the
       panel: the sheet's reveal is a grid whose one column is as wide as
       what is in it. */
    width: 0;
}

.vntv-snd-clip-title {
    overflow: hidden;
    color: var(--vntv-fg);
    font-size: 12.5px;
    line-height: 16px;
    text-overflow: ellipsis;
    white-space: nowrap;
}

.vntv-snd-clip-title[data-untitled="true"] {
    color: var(--vntv-second);
}

.vntv-snd-clip-who {
    overflow: hidden;
    color: var(--vntv-second);
    font-size: 11px;
    text-overflow: ellipsis;
    white-space: nowrap;
}

/* The last step: the tile as it will be, its emoji and its name. */
.vntv-snd-finish {
    display: flex;
    flex-direction: column;
    gap: 14px;
}

.vntv-snd-made {
    display: flex;
    align-items: center;
    gap: 14px;
    min-width: 0;
}

.vntv-snd-sample {
    flex: 0 0 72px;
    grid-template-columns: minmax(0, 1fr);
}

.vntv-snd-sample .vntv-snd-tile {
    cursor: default;
}

.vntv-snd-sample .vntv-snd-tile[data-empty="true"] .vntv-snd-name {
    color: var(--vntv-second);
}

.vntv-snd-fields {
    display: flex;
    flex: 1 1 auto;
    flex-direction: column;
    gap: 8px;
    min-width: 0;
}

.vntv-snd-emoji-button {
    display: flex;
    align-items: center;
    gap: 8px;
    min-width: 0;
    height: 34px;
    margin: 0;
    padding: 0 10px 0 6px;
    border: 1px solid var(--vntv-line);
    border-radius: 4px;
    background: var(--vntv-bg);
    color: var(--vntv-fg);
    cursor: pointer;
    font: inherit;
}

.vntv-snd-emoji-small {
    width: 22px;
    height: 22px;
}

.vntv-snd-emoji-name {
    flex: 1 1 auto;
    min-width: 0;
    overflow: hidden;
    color: var(--vntv-fg);
    font-size: 12px;
    text-align: left;
    text-overflow: ellipsis;
    white-space: nowrap;
}

.vntv-snd-emoji-change {
    flex: 0 0 auto;
    color: var(--vntv-second);
    font-size: 11px;
}

.vntv-snd-name-field {
    position: relative;
    display: block;
    min-width: 0;
}

.vntv-snd-name-field .vntv-input {
    width: 100%;
    padding-right: 56px;
}

.vntv-snd-left {
    position: absolute;
    top: 50%;
    right: 10px;
    color: var(--vntv-second);
    font-size: 11px;
    font-variant-numeric: tabular-nums;
    transform: translateY(-50%);
    pointer-events: none;
}

.vntv-snd-left[data-low="true"] {
    color: var(--vntv-danger);
}

.vntv-snd-submit {
    width: 100%;
}

/* The emoji chooser: Mattermost's search, and RECENT and SUGGESTED before
   anything is typed. Six to a row at 304 px, more where there is room. */
.vntv-snd-chooser {
    display: flex;
    flex-direction: column;
    gap: 8px;
}

.vntv-snd-chooser .vntv-input {
    width: 100%;
}

.vntv-snd-picks-scroll {
    max-height: 236px;
}

.vntv-snd-picks-head {
    margin: 4px 0 6px;
    color: var(--vntv-second);
    font-family: var(--vntv-display);
    font-size: 10px;
    font-weight: 600;
    letter-spacing: 0.14em;
    text-transform: uppercase;
}

.vntv-snd-picks {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(36px, 1fr));
    gap: 4px;
}

.vntv-snd-pick {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    aspect-ratio: 1 / 1;
    margin: 0;
    padding: 0;
    border: none;
    border-radius: 4px;
    background: transparent;
    cursor: pointer;
}

.vntv-snd-pick[data-chosen="true"] {
    background: rgba(var(--vntv-accent-rgb), 0.08);
    box-shadow: inset 0 0 0 2px var(--vntv-accent);
}

.vntv-snd-pick-image {
    display: block;
    width: 24px;
    height: 24px;
    object-fit: contain;
    pointer-events: none;
}

@media (hover: hover) {
    .vntv-snd-tile:not([aria-disabled="true"]):hover .vntv-snd-face {
        border-color: var(--vntv-line-focus);
        background: var(--vntv-fill-hover);
    }

    .vntv-snd-addtile:hover .vntv-snd-face {
        border-color: rgba(var(--vntv-fg-rgb), 0.48);
        color: var(--vntv-fg);
    }

    .vntv-snd-cell:hover .vntv-snd-more {
        opacity: 1;
    }

    .vntv-snd-quiet:not([data-armed="true"]):hover {
        background: rgba(var(--vntv-danger-rgb), 0.16);
    }

    .vntv-snd-menu-item:hover {
        background: var(--vntv-fill-hover);
    }

    .vntv-snd-source:hover,
    .vntv-snd-clip:not(.vntv-snd-clip-chosen):hover {
        border-color: var(--vntv-line-focus);
        background: var(--vntv-fill);
    }

    .vntv-snd-pick:hover {
        background: var(--vntv-fill-hover);
    }

    .vntv-snd-emoji-button:hover {
        border-color: rgba(var(--vntv-fg-rgb), 0.48);
    }
}

/* Touch, where nothing can point: the dots are always there. */
@media (hover: none) and (pointer: coarse) {
    .vntv-snd-more {
        opacity: 0.85;
    }
}

/* ---- 3.2.0: the rest ---- */

/* A plan link to a plan no longer listed (PlanLinkRoute.tsx): the sentence,
   and the way on to Mattermost under it. */
.vntv-planlink {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 12px;
}

.vntv-planlink p {
    margin: 0;
}

/* The soundboard's sheet takes focus as it opens, so Tab goes on into it;
   the sheet itself shows no ring for that. */
.vntv-sheet-sounds:focus {
    outline: none;
}

/* A sound file dragged over the first step of adding one. */
.vntv-snd-add[data-dropping="true"] .vntv-snd-sources {
    border-radius: 12px;
    outline: 2px dashed rgba(var(--vntv-accent-rgb), 0.7);
    outline-offset: 4px;
}

/* System Console: the sounds on the board, each with its emoji. */
.vntv-admin-sound-emoji {
    display: flex;
    flex: 0 0 auto;
    align-items: center;
    justify-content: center;
    width: 36px;
    height: 36px;
    border-radius: 8px;
    background: rgba(127, 127, 127, 0.12);
}

.vntv-admin-sound-emoji .vntv-snd-emoji {
    width: 26px;
    height: 26px;
}

.vntv-admin-sound-bad {
    margin-left: 8px;
    color: var(--error-text, #d24b4e);
    font-size: 12px;
    font-weight: 600;
}

/* ---- Less motion ----
   LAST in the sheet, and it has to be. These rules add no specificity to the
   ones they switch off, so they only win by coming later; this block used to
   sit near the top, and most of it — the static, the roll, the CRT effects,
   the + turning into an x — lost to the rules below it and did nothing. */
@media (prefers-reduced-motion: reduce) {
    .vntv-link-cta, .vntv-bar-save, .vntv-bar-tool { transition: none; }
    .vntv-bar-clips[data-state="live"] .vntv-bar-dot::after { animation: none; }
    .vntv-onnow, .vntv-sheet-close, .vntv-sheet-action { transition: none; }
    .vntv-live-add { animation: none; }
    .vntv-live-pulse { animation: none; }
    .vntv-live-layer { transition: none; }
    .vntv-live-card:hover .vntv-live-layer { transform: none; }
    .vntv-upnext-remove { transition: none; }
    .vntv-trophy { transition: none; }
    .vntv-trophy-tab { transition: none; }
    .vntv-upnext-dot { animation: none; }

    /* The static and the roll are the point of the no-signal screen, so they
       are stilled rather than removed: the texture stays, the movement stops. */
    .vntv-snow { animation: none; }
    .vntv-roll { animation: none; opacity: 0.4; }

    /* The panel skips the boot and the switch-off sequences itself when it
       sees this; these are the belt to go with those braces. */
    .vntv-panel[data-boot="true"]::before { display: none; }
    .vntv-screen[data-power] { animation: none; }
    .vntv-screen[data-power]::after { animation: none; }
    .vntv-tile[data-leaving="true"] { animation: none; opacity: 0; }
    .vntv-screen[data-ghost="true"] .vntv-ghost { animation: none; }
    .vntv-player-dead[data-arrive="fade"] { animation: none; }
    .vntv-upnext-area { animation: none; }
    .vntv-upnext-arrow { animation: none; transition: none; }
    .vntv-hud-control { transition: none; }
    .vntv-field-save[data-busy="true"] { animation: none; }
    .vntv-marquee[data-moving="true"] .vntv-marquee-track { animation: none; }
    .vntv-factrow-dot { animation: none; }
    .vntv-suggest { animation: none; }
    .vntv-picked { animation: none; }
    .vntv-suggest-spinner { animation-duration: 2s; }
    .vntv-suggest-wait-thumb, .vntv-suggest-wait-lines span { animation: none; }
    .vntv.vntv-mediaprofile { animation: none; }
    .vntv-upnext-art { transition: none; }
    .vntv-reveal[data-armed="true"] { transition: none; }
    .vntv-reveal[data-armed="true"] .vntv-reveal-inner { transition: none; }
    .vntv-reveal-playing[data-armed="true"] { transition: none; }

    /* The small player arrives, changes size, settles and goes at once, and
       the line counting down to the panel coming back is not drawn at all. */
    .vntv.vntv-mini { animation: none; }
    .vntv-mini[data-flip] { animation: none; }
    .vntv-mini[data-leaving="true"] { animation: none; opacity: 0; }
    .vntv-mini[data-snapping="true"] { transition: none; }
    .vntv-mini-drain { display: none; }
    .vntv-mini-back { transition: none; }
    .vntv-return { animation: none; }

    /* Clips: the loading cards are still, and nothing grows or fades. */
    .vntv-clips-ghost-pic, .vntv-clips-ghost-line { animation: none; }
    .vntv-clips-btn, .vntv-clips-pill { transition: none; }
    .vntv-clipcard-pic, .vntv-clipcard-thumb { transition: none; }
    .vntv-clipcard-play:hover .vntv-clipcard-thumb { transform: none; }
    button.vntv-upvote { transition: none; }
    .vntv-switch-track, .vntv-switch-thumb, .vntv-clips-streamer-wipe { transition: none; }

    /* The scissors' pulse while cutting, the editor's publishing bar (held
       still, all the way across) and its play button. */
    .vntv-hud-busy svg { animation: none; }
    .vntv-clipedit-progress-bar { animation: none; left: 0; width: 100%; opacity: 0.6; }
    .vntv-clipedit-bigplay { transition: none; }
    .vntv-clipedit-bigplay:hover { transform: none; }
    .vntv-clipedit-trim, .vntv-clipedit-title { transition: none; }

    /* A shared clip's card no longer breathes while it is asked about. */
    .vntv-postclip[data-state="loading"] .vntv-postclip-placeholder { animation: none; }

    /* The lock opens at once: no swinging padlock, no glass clearing and no
       fade. */
    .vntv-lock-shackle, .vntv-lock-bar, .vntv-textkey { transition: none; }
    .vntv-lock-emblem, .vntv-lock-tool { transition: none; }
    .vntv-lock[data-unlocking="true"] .vntv-lock-stage, .vntv-lock[data-unlocking="true"] .vntv-lock-dock { animation: none; }
    /* The System Console's pills change colour at once. */
    .vntv-admin .vntv-pill { transition: none; }

    /* Twitch: the tile's buttons change at once. Nothing of Twitch's moves
       of the panel's own accord; its player is its own. */
    .vntv-twitch-button { transition: none; }

    /* 3.2.0: the picker's chips and segments change at once, and a new
       achievement's toast is simply there and then gone. */
    .vntv-awardchip, .vntv-segment { transition: none; }
    .vntv-achtoast, .vntv-achtoast[data-leaving="true"] { animation: none; }

    /* Up next's share glyph and a shared clip's thumb change at once. */
    .vntv-upnext-share { transition: none; }
    button.vntv-postclip-up { transition: none; }

    /* The soundboard: tiles change at once and a new one does not pulse.
       The ring round a playing tile and the cooldown's sweep stay: they
       say how long, and they move with the sound, not for show. */
    .vntv-snd-face, .vntv-snd-more, .vntv-snd-source { transition: none; }
    .vntv-snd-tile[data-fresh="true"] .vntv-snd-face { animation: none; }
    .vntv-snd-tile:active:not([aria-disabled="true"]) .vntv-snd-face { transform: none; }

    /* The clip player: its loading ring turns slowly, and its line and its
       handle change at once. */
    .vntv-clip-ring { animation-duration: 2s; }
    .vntv-clip-scrub-track, .vntv-clip-scrub-handle { transition: none; }
}
`;

export const CODE_STYLE_ELEMENT_ID = 'matty-code-styles';

// Mattermost's codeTheme setting is a fixed choice of four highlight.js themes,
// the darkest of which is monokai at #272822 — a warm grey that sits awkwardly
// on a pitch-black canvas. Only CSS can put a code block on black, so this is
// offered as an explicit admin opt-in rather than done by default.
const CODE_CSS = `
.hljs {
    background: #0a0a0c !important;
}

.post-code,
.post-code--wrap,
.post-body pre {
    border: 1px solid rgba(var(--center-channel-color-rgb, 63, 67, 80), 0.16) !important;
    border-radius: 4px;
}
`;

export function injectCodeStyles(): void {
    if (document.getElementById(CODE_STYLE_ELEMENT_ID)) {
        return;
    }

    const element = document.createElement('style');
    element.id = CODE_STYLE_ELEMENT_ID;
    element.textContent = CODE_CSS;
    document.head.appendChild(element);
}

export function removeCodeStyles(): void {
    document.getElementById(CODE_STYLE_ELEMENT_ID)?.remove();
}

export function injectStyles(): void {
    if (document.getElementById(STYLE_ELEMENT_ID)) {
        return;
    }

    const element = document.createElement('style');
    element.id = STYLE_ELEMENT_ID;
    element.textContent = CSS;
    document.head.appendChild(element);
}

export function removeStyles(): void {
    document.getElementById(STYLE_ELEMENT_ID)?.remove();
    removeCodeStyles();
}
