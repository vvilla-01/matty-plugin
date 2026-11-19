import React from 'react';

import {clearNowPlaying, fetchMyProfile, fetchNowPlaying, saveMyProfile, unlinkStream} from '../client';
import {pollWhileShown, refreshSnapshot, useSnapshot} from '../store';
import {getIdentity, refreshIdentity, setIdentity, takeLinkRequest, useIdentity, useLinkRequest} from '../identity';
import {noteSelfUnlink} from '../access';
import {bannerOf} from '../banner';
import {isPasteKey, keepTokenKeys} from '../focusTrap';
import {useTwoClicks} from '../twoClicks';
import {streamerView} from '../clipLogic';
import {useHeldTails} from '../clips';
import {
    MAX_WATCHING,
    reconcile,
    removeWatching,
    setAudio,
    useAudio,
    useWatching,
    watchOnly,
} from '../watching';
import {widenWhileOpen} from '../rhsWidth';
import {captureStillsWhileOpen} from '../stills';
import {fitPopoutWindow} from '../popout';
import {isTwitchHandle, measureStageRow, watchOrOpen} from '../twitch';
import {liveCards, liveHandlesOf} from '../twitchLogic';
import {voiceOf} from '../voice';
import {useFocusHome} from '../focusHome';
import type {Me, NowPlaying, Profile, ScheduleItem, Stream} from '../types';

import {Player, WaitingTile} from './Player';
import {TwitchPlayer} from './TwitchPlayer';
import {LiveCard} from './LiveCard';
import {UpNext} from './UpNext';
import {VoiceStrip} from './VoiceStrip';
import {PlayingStrip} from './PlayingStrip';
import {NoSignalTile} from './NoSignal';
import {HashtagSearchHost} from './HashtagSearch';
import {CalendarPlusIcon, CheckIcon, EyeIcon, LinkIcon, ScissorsIcon} from './icons';
import {Reveal} from './Reveal';
import {ReturnStrip} from './ReturnStrip';
import {ScheduleControls} from './ScheduleControls';
import {StreamerClips} from './StreamerClips';
import {WhatsOnChip, WhatsOnPicker} from './WhatsOn';
import {AdminAlerts, consolePath} from './AdminAlerts';
import {TokenBar} from './LockScreen';
import {SheetHead} from './SheetHead';
import {AccountsDisconnectKey} from './AccountsKey';
import {AccountFlow, AccountsList, TwitchFlow} from './AccountSheet';
import {AccountNotices} from './AccountNotices';
import {SHEET_HEAD_WITHOUT, SHEET_HEAD_WITH_ACCOUNTS, accountRowsFor, playingNow, relinkKeyWithAccounts} from '../accountsLogic';
import {personName} from '../accounts';
import type {AnyService} from '../accountsLogic';
import {useAccountsFor} from '../useAccounts';
import {AchievementToast} from './AchievementToast';
import {SoundboardSheet, SoundboardTool} from './Soundboard';
import {TryAgain} from './TryAgain';
import {BOOT_MS, CRT_OFF_MS, GLIDE_MS, LEAVE_MS, reducedMotion} from '../motion';
import {useArtwork, useName} from '../name';
import {composing} from '../keys';
import {cameBack} from '../away';

// How often the footer asks again what the server has for What's on?, while
// something is said and the streamer is off the air. The server takes it down
// by itself a little after their stream ends, and nothing it pushes says so.
const ON_NOW_RECHECK_MS = 60000;

// Ids for the footer's sheets, and for what the scissors in its bar say about
// clips of your stream. From a counter (see TitleSearch); one panel to a
// window, but a counter costs nothing and never collides.
let nextFooterId = 0;

// The footer is one bar, and everything it can open is a sheet that rises out
// of it, one at a time: What's on?, planning a stream, clips of your stream,
// the bearer token bar — for somebody linked, their stream and their
// connected accounts (Twitch, Steam and Hytale, design/3.4.0.md 5.2); for
// somebody let in, linking — and the soundboard, for somebody linked while
// they are in voice (design/3.2.0.md 7.13). '' while none is open.
type Sheet = '' | 'onnow' | 'plan' | 'clips' | 'link' | 'sounds';

// How long after the switch is pressed here the reader's own stream may
// still say what it said before: the server starts recording again at its
// next poll of Broadcast Box, which can be set as far apart as five minutes,
// and the panel hears of it a moment later.
const JUST_SWITCHED_MS = 330000;

type Props = {
    // What the reader may do here (design/lock-screen.md 5): 'linked' is the
    // whole panel; 'letIn' is watch only — no planning, no Join, no What's
    // on?, no stream title, no streamer controls, no Pop out and no preview
    // stills of their own, and in the footer one quiet WATCH ONLY line with
    // a key to link their stream. PanelGate mounts a fresh panel when this
    // changes, so everything this loads once is loaded for the rights it has.
    access: 'linked' | 'letIn';

    // The panel's root, for PanelGate's rule about focus when it swaps the
    // panel for the lock and back.
    onRoot?: (element: HTMLDivElement | null) => void;

    // The newest read of "me" failed, or the store did not answer, after a
    // definite answer: the panel stays, with its streams, and says so quietly.
    accessUnchecked?: boolean;
};

export const Sidebar: React.FC<Props> = ({access, onRoot, accessUnchecked}) => {
    const snapshot = useSnapshot();
    const watching = useWatching();
    const audio = useAudio();
    const linked = access === 'linked';

    // Who is reading, as the page knew it when the panel mounted: PanelGate
    // has just read it again, and the panel follows every change of it.
    const [me, setMe] = React.useState<Me | null>(getIdentity);
    const [profile, setProfile] = React.useState<Profile | null>(null);

    // Who is reading as the plugin knew it when the page loaded, which the
    // panel goes by in the moment between opening and its own "me" landing.
    const known = useIdentity();

    // The panel coming up when it is opened: a scan line sweeping down it. On
    // for the length of the sweep and then off for good, so nothing a re-render
    // does can play it a second time.
    //
    // Not when it is coming back from a thread or a search that took its
    // place (away.ts): it was never switched off, and its pictures were
    // playing in the small player all along. cameBack() answers the same
    // whether Mattermost renders this before or after the panel hears about
    // the change.
    const [booting, setBooting] = React.useState(() => !reducedMotion() && !cameBack());
    React.useEffect(() => {
        const timer = window.setTimeout(() => setBooting(false), BOOT_MS + 80);

        return () => window.clearTimeout(timer);
    }, []);

    // As wide as Mattermost's side panel goes while this is in it, until the
    // person drags it to a width of their own. See rhsWidth.ts.
    React.useEffect(widenWhileOpen, []);

    // Preview stills for the Live Now cards, taken while the panel is open,
    // by linked people only: a still is shown to everybody on the card, which
    // is publishing rather than watching (design/lock-screen.md question 6).
    // Everybody's stills still show. See stills.ts.
    React.useEffect(() => (linked ? captureStillsWhileOpen() : undefined), [linked]);

    // In a window of its own, as tall as the window and no taller, so the
    // footer stays in view however wide the window is made. See popout.ts.
    const panelRef = React.useRef<HTMLDivElement | null>(null);
    React.useEffect(() => fitPopoutWindow(panelRef.current), []);

    // Tiles being switched off, and how far along: 'off' while the tube
    // collapses, 'leaving' while it slides away behind the tiles still up.
    // Mirrored into a ref because the timers that move a tile along read it
    // long after the render that scheduled them.
    const [closing, setClosing] = React.useState<Record<string, 'off' | 'leaving'>>({});
    const closingRef = React.useRef(closing);
    closingRef.current = closing;

    // Where a leaving tile was, so it can be lifted out of the grid — and the
    // tiles still up can close the gap — while it goes on sliding away.
    const [leavingAt, setLeavingAt] = React.useState<Record<string, Box>>({});

    const gridRef = React.useRef<HTMLDivElement | null>(null);
    const stageRef = React.useRef<HTMLDivElement | null>(null);

    // How wide a row of tiles is, for a Twitch stream started from a card:
    // whether its tile would be wide enough for Twitch's player, or the
    // theatre should open in the same click (twitch.ts stageWidth). Kept
    // after the panel goes, for a Watch from somebody's profile.
    React.useLayoutEffect(() => measureStageRow(stageRef.current), []);

    // Which of the footer's sheets is open, if any. One at a time: opening one
    // closes whichever was open, so the footer never stacks two forms.
    const [sheet, setSheet] = React.useState<Sheet>('');
    const [footerIds] = React.useState(() => {
        nextFooterId += 1;

        return {
            onNow: `villanet-whatson-${nextFooterId}`,
            plan: `villanet-plan-${nextFooterId}`,
            clips: `villanet-clipsheet-${nextFooterId}`,
            clipState: `villanet-clipstate-${nextFooterId}`,
            link: `villanet-link-${nextFooterId}`,
            sounds: `villanet-soundsheet-${nextFooterId}`,
        };
    });

    // Closes the sheet named, if it is still the one open: a save that lands
    // after another sheet was opened leaves that one alone.
    const closeSheet = React.useCallback((which: Sheet) => {
        setSheet((current) => (current === which ? '' : current));
    }, []);

    // Planning a stream: the form in its sheet, keyed per opening, so every
    // opening is a fresh form.
    const planning = sheet === 'plan';
    const [planKey, setPlanKey] = React.useState(0);

    // The plan being changed, when the form is open on one of your own rather
    // than on a new one.
    const [editing, setEditing] = React.useState<ScheduleItem | null>(null);

    const editingLink = sheet === 'link';

    // Unlink, in the relink form: two presses, and what went wrong if it did.
    const [linkError, setLinkError] = React.useState('');
    const [unlinking, setUnlinking] = React.useState(false);
    const unlinkTwice = useTwoClicks();
    const linkBarRef = React.useRef<HTMLInputElement | null>(null);

    const [editingProfile, setEditingProfile] = React.useState(false);
    const [draftNote, setDraftNote] = React.useState('');
    const [profileError, setProfileError] = React.useState('');
    const [savingProfile, setSavingProfile] = React.useState(false);

    // What you said is on, from What's on? in the footer's bar: null for
    // nothing, undefined until it has loaded. The picker in its sheet, keyed
    // per opening like the planning form. See WhatsOn.tsx.
    const [onNow, setOnNow] = React.useState<NowPlaying | null | undefined>(undefined);
    const choosingOnNow = sheet === 'onnow';
    const [onNowKey, setOnNowKey] = React.useState(0);
    const [clearingOnNow, setClearingOnNow] = React.useState(false);
    const [onNowError, setOnNowError] = React.useState('');

    const refresh = React.useCallback(() => {
        refreshSnapshot().catch(() => {
            // The next tick will try again; the panel keeps the last snapshot.
        });
    }, []);

    // null only while it is loading: a profile that could not be fetched is an
    // unavailable one, with a sentence saying so, so the footer can tell
    // "still coming" from "not coming".
    const loadProfile = React.useCallback(() => {
        fetchMyProfile().then(setProfile).catch(() => setProfile({
            available: false,
            motd: '',
            isPublic: true,
            message: 'Your stream title could not be loaded just now.',
        }));
    }, []);

    // Like the profile: loaded once the link is known, and only for somebody
    // linked, who is the only person the server answers. A failure reads as
    // nothing said, which the chip can still offer to set.
    const loadNowPlaying = React.useCallback(() => {
        fetchNowPlaying().then((answer) => setOnNow(answer.nowPlaying)).catch(() => setOnNow(null));
    }, []);

    // The snapshot, fetched now and every 30 seconds while the panel is up.
    // Shared with anything else that shows streams, so the two never poll
    // twice; see store.ts.
    React.useEffect(pollWhileShown, []);

    // The stream title and What's on?, for somebody linked: nobody else has
    // either (the server refuses them, and the footer shows neither). "me"
    // itself is not fetched here: PanelGate has just asked for it, and the
    // panel follows the identity it keeps.
    React.useEffect(() => {
        if (linked) {
            loadProfile();
            loadNowPlaying();
        }
    }, [linked, loadProfile, loadNowPlaying]);

    // From here on the rest of the page goes by the panel's "me" when the
    // panel changes it: a relink from the footer, the streamer's switch.
    React.useEffect(() => {
        if (me && me !== getIdentity()) {
            setIdentity(me);
        }
    }, [me]);

    // And the other way: "me" fetched again from outside the panel takes the
    // panel's place too. That is refreshIdentity(), which runs when a clip
    // route says the reader is not linked (their link was undone elsewhere,
    // or somebody else proved their stream key) and when somebody signs in
    // without the page loading again; without this the panel went on showing
    // the old link — its scissors, its footer — until it was
    // closed and opened again.
    //
    // Only when the identity itself changes, never when the panel's own "me"
    // does: a link made from the footer is the panel's newer news, which the
    // effect above is about to hand on, and must not be put back by the
    // identity it has not reached yet. Nothing "me" says about the stream is
    // kept from before: the title and What's on? are fetched again for the
    // stream now linked, or put away with the link.
    const lastKnown = React.useRef(known);
    React.useEffect(() => {
        if (known === lastKnown.current) {
            return;
        }

        lastKnown.current = known;
        if (!known || known === me) {
            return;
        }

        const relinked = (me?.streamKey || '') !== known.streamKey;
        setMe(known);
        if (relinked && linked) {
            loadProfile();
            if (known.streamKey) {
                loadNowPlaying();
            } else {
                setOnNow(null);
            }
        }
    }, [known, me, linked, loadProfile, loadNowPlaying]);

    const streams = snapshot.streams;

    // Give up tiles whose stream has been gone a while, so the panel does not
    // sit on a dead player. reconcile is deliberately silent when nothing
    // changed, which is what stops this effect re-triggering itself.
    React.useEffect(() => {
        reconcile(streams.map((stream) => stream.handle));
    }, [streams]);

    // Remember each stream as it was last seen, so a tile waiting for a stream
    // to come back still says whose it is — name, face, what they were
    // streaming — rather than going anonymous. Written in an effect rather than
    // during render, which must stay side-effect free.
    const seenRef = React.useRef<Record<string, Stream>>({});
    React.useEffect(() => {
        streams.forEach((stream) => {
            seenRef.current[stream.handle] = stream;
        });
    }, [streams]);

    // A tile whose stream this panel never saw live — the watch list outlives
    // the panel — is named as the server named the stream when it stopped,
    // while its end is held.
    const tiles = watching.map((handle) => ({
        handle,
        title: seenRef.current[handle]?.title || snapshot.clipTails?.find((tail) => tail.handle === handle)?.title || 'Stream',
        seen: seenRef.current[handle] || null,
        stream: streams.find((candidate) => candidate.handle === handle) || null,
    }));

    // Two tiles is where the set stops being one thing to look at and starts
    // being a wall; anything under it is then below the fold.
    //
    // Counted INCLUDING a tile that is on its way out, so the rail does not
    // jump from the top to the bottom while that tile is still sliding away.
    // It moves once the tile has gone, as a separate, second settle — two
    // readable motions rather than four at once.
    const manyTiles = tiles.length > 1;

    // Planning a stream, and changing one, take a stream of your own linked —
    // the same rule as joining one, and the server holds to it. Somebody let
    // in can read what is planned, and cancel their own (a takeback), as a
    // system admin can cancel anybody's (Up next).
    const reader = me || known;
    const canPlan = linked;

    // The Up next row is there only while something is planned, for everybody
    // (Victor's request of 2026-10-05). An empty queue used to keep it on
    // screen as placeholders with a + at their start: a row of the panel spent
    // on nothing. Planning starts from the calendar in the footer's bar now,
    // which is there either way, and the plan it saves brings the row in.
    const nothingPlanned = (snapshot.schedule || []).length === 0;
    const upNextShown = !nothingPlanned;

    // Unlinked while a sheet that needs the link was open: the planning form
    // can no longer save, so it closes rather than sitting there waiting to be
    // refused, and What's on? with it, which the server took down with the
    // link. The clip controls go too: the server answers for another stream
    // now, or none. Focus is in the footer at that moment, where the
    // unlinking happened, so none is lost.
    React.useEffect(() => {
        if (!canPlan) {
            setSheet((current) => (current === 'link' ? current : ''));
            setEditing(null);
        }
    }, [canPlan]);

    // Whether the reader's own stream is on the air. While it is, the server
    // leaves What's on? alone; once it is not, the server takes it down after
    // the offline grace without telling anybody, so the footer asks again
    // now and then rather than go on showing a choice that has gone. Stops at
    // the first failure, rather than retrying an endpoint that is refusing.
    const ownOnAir = Boolean(reader?.userId && streams.some((stream) => stream.userId === reader.userId && !stream.alwaysOn));
    const saying = Boolean(onNow);
    React.useEffect(() => {
        if (!saying || ownOnAir || !canPlan) {
            return undefined;
        }

        let stopped = false;
        const timer = window.setInterval(() => {
            fetchNowPlaying().then((answer) => {
                if (!stopped) {
                    setOnNow(answer.nowPlaying);
                }
            }).catch(() => window.clearInterval(timer));
        }, ON_NOW_RECHECK_MS);

        return () => {
            stopped = true;
            window.clearInterval(timer);
        };
    }, [saying, ownOnAir, canPlan]);

    // Whether keyboard focus is in the Up next row. When the row goes — its
    // last plan cancelled — focus is put on the calendar in the footer's bar,
    // the way to plan another, rather than falling out of the panel to the
    // top of the page.
    //
    // Focus that goes nowhere because the button holding it was switched off
    // has not left the row; it has fallen out of it. That is the × on a plan
    // being cancelled, which is disabled while the cancel is on its way — and
    // when that was the last plan, the row goes a moment later, and focus
    // still has to be handed on from it.
    const rowFocus = React.useRef(false);
    const planButtonRef = React.useRef<HTMLButtonElement | null>(null);

    // What opened the planning form — the calendar, or the pencil on a card —
    // for focus to go back to when it closes.
    const planFrom = React.useRef<HTMLElement | null>(null);
    const trackRowFocus = {
        onFocus: () => {
            rowFocus.current = true;
        },
        onBlur: (event: React.FocusEvent<HTMLElement>) => {
            const next = event.relatedTarget as Node | null;
            if (next && event.currentTarget.contains(next)) {
                return;
            }
            if (!next && (event.target as Element).matches(':disabled')) {
                return;
            }
            rowFocus.current = false;
        },
    };

    // Where the row is: above the tiles, below them, or nowhere. Any change
    // moves the tiles, which glide there rather than jump.
    const upNextPlace = (() => {
        if (!upNextShown) {
            return 'none';
        }

        return manyTiles ? 'above' : 'below';
    })();

    // The tiles that still take up room in the grid.
    const placedTiles = tiles.filter(({handle}) => closing[handle] !== 'leaving');

    // Which tiles exist, readable from the timers below without closing over a
    // stale render.
    const tileHandlesRef = React.useRef<Record<string, true>>({});
    tileHandlesRef.current = Object.fromEntries(tiles.map(({handle}) => [handle, true as const]));


    // Switching a tile off.
    //
    // Alone, it is a television turning off: the tube collapses to a line and a
    // dot, and then the idle set is back. With others still up, it then slides
    // up BEHIND them and fades, while they glide into the space it left. The
    // tile is only taken out of the watching list once all of that has played
    // — the list is what the rest of the panel reads, and the rest of the
    // panel should not move until the tile has.
    const closeTile = React.useCallback((handle: string) => {
        if (closingRef.current[handle]) {
            return;
        }

        if (reducedMotion()) {
            removeWatching(handle);

            return;
        }

        const finish = () => {
            removeWatching(handle);
            setClosing((current) => {
                const {[handle]: _gone, ...rest} = current;

                return rest;
            });
            setLeavingAt((current) => {
                const {[handle]: _gone, ...rest} = current;

                return rest;
            });
        };

        setClosing((current) => ({...current, [handle]: 'off'}));

        window.setTimeout(() => {
            const grid = gridRef.current;
            const tile = grid?.querySelector<HTMLElement>(`[data-handle="${CSS.escape(handle)}"]`);
            const others = Object.keys(tileHandlesRef.current).filter(
                (other) => other !== handle && !closingRef.current[other],
            );

            if (!grid || !tile || others.length === 0) {
                finish();

                return;
            }

            // Lifted out of the grid exactly where it stands, so the grid
            // reflows around it while it is still visibly there.
            const gridBox = grid.getBoundingClientRect();
            const box = tile.getBoundingClientRect();
            setLeavingAt((current) => ({
                ...current,
                [handle]: {x: box.left - gridBox.left, y: box.top - gridBox.top, w: box.width, h: box.height},
            }));
            setClosing((current) => ({...current, [handle]: 'leaving'}));

            window.setTimeout(finish, LEAVE_MS);
        }, CRT_OFF_MS);
    }, []);

    // A panel closed halfway through a tile switching off would otherwise leave
    // that tile in the watching list for ever — the timer that removes it dies
    // with the panel, and the list outlives it.
    React.useEffect(() => () => {
        Object.keys(closingRef.current).forEach((handle) => removeWatching(handle));
    }, []);

    // The glide. After every render, where each tile sits inside the grid and
    // where the grid sits in the panel are written down; on a render where a
    // tile has just left the layout, or the rail has changed sides, anything
    // that moved is sent back to where it was and animated home. Measured
    // relative to the grid and to the scrolled panel, so scrolling in between
    // is not mistaken for movement.
    const lastTiles = React.useRef<Record<string, Box>>({});
    const lastPlaced = React.useRef('');
    const lastStage = React.useRef<Box | null>(null);
    const lastLive = React.useRef<Box | null>(null);
    const liveRef = React.useRef<HTMLDivElement | null>(null);
    const lastPlace = React.useRef(upNextPlace);

    React.useLayoutEffect(() => {
        const placedKey = placedTiles.map(({handle}) => handle).join('|');
        const shrank = lastPlaced.current !== '' &&
            placedKey.split('|').filter(Boolean).every((handle) => lastPlaced.current.split('|').includes(handle)) &&
            placedKey.length < lastPlaced.current.length;
        const moved = lastPlace.current !== upNextPlace;
        const still = reducedMotion();

        const grid = gridRef.current;
        const nextTiles: Record<string, Box> = {};
        if (grid) {
            const gridBox = grid.getBoundingClientRect();
            grid.querySelectorAll<HTMLElement>('.vntv-tile').forEach((element) => {
                const handle = element.dataset.handle || '';
                const box = element.getBoundingClientRect();
                const here = {x: box.left - gridBox.left, y: box.top - gridBox.top, w: box.width, h: box.height};
                nextTiles[handle] = here;

                const before = lastTiles.current[handle];
                if (shrank && !still && before && !element.dataset.leaving) {
                    glide(element, before, here);
                }
            });
        }

        const stage = stageRef.current;
        const body = stage?.closest('.vntv-body');
        let nextStage: Box | null = null;
        if (stage && body) {
            const stageBox = stage.getBoundingClientRect();
            const bodyBox = body.getBoundingClientRect();
            nextStage = {x: 0, y: (stageBox.top - bodyBox.top) + body.scrollTop, w: stageBox.width, h: stageBox.height};

            if (moved && !still && lastStage.current) {
                glide(stage, lastStage.current, nextStage);
            }
        }

        // What is under the stage moves with it, or it would jump into place
        // while the stage was still gliding, and be painted over by it: the
        // Live now list.
        const follow = (element: HTMLElement | null, last: Box | null): Box | null => {
            if (!element || !body) {
                return null;
            }

            const box = element.getBoundingClientRect();
            const bodyBox = body.getBoundingClientRect();
            const next = {x: 0, y: (box.top - bodyBox.top) + body.scrollTop, w: box.width, h: box.height};
            if (moved && !still && last) {
                glide(element, last, next);
            }

            return next;
        };
        const nextLive = follow(liveRef.current, lastLive.current);

        // The row went, or moved, while it had focus. While it is there,
        // somewhere new, the rail itself takes focus; once it has gone — the
        // last plan on it went — the calendar in the footer's bar does, which
        // is the way to plan another, or else the panel itself.
        const lost = !document.activeElement || document.activeElement === document.body;
        if (moved && rowFocus.current && lost) {
            const rail = panelRef.current?.querySelector<HTMLElement>('.vntv-upnext-rail') || null;
            const next = upNextPlace === 'none' ? planButtonRef.current : rail;

            if (next) {
                next.focus({preventScroll: true});
            } else {
                rowFocus.current = false;
                panelRef.current?.focus({preventScroll: true});
            }
        }

        lastTiles.current = nextTiles;
        lastPlaced.current = placedKey;
        lastStage.current = nextStage;
        lastLive.current = nextLive;
        lastPlace.current = upNextPlace;
    });

    // Who is on air, for the Up next rail: a card whose time has come says
    // "Live now" rather than counting up from a start that has happened, and
    // it can be clicked to open that stream. The handle is what the rail needs
    // for both — the second one especially, since it is what the player is
    // addressed by.
    //
    // Paused and always-on are both excluded, the same test the profile
    // popover and the live badge use. A paused publisher is still listed by
    // Broadcast Box, and a 24/7 channel is always listed — either one would
    // have the rail announcing a broadcast while the stream card beside it
    // drew the idle dot for the same person.
    //
    // One handle a person: never a Twitch entry covered by their Broadcast
    // Box card, and the Broadcast Box stream before the Twitch one. The server
    // decides (the snapshot's people index), so every client agrees.
    const liveHandles = React.useMemo(() => liveHandlesOf(snapshot.people), [snapshot.people]);

    // What Live now lists: one card a person, a Twitch entry covered by its
    // owner's Broadcast Box card left out (design/twitch-live.md 5.6). The
    // tiles still go by every handle in the snapshot.
    const cards = React.useMemo(() => liveCards(streams), [streams]);

    // Watching from a card, the round button or its body: a Twitch stream
    // that cannot play here opens on twitch.tv, and one whose tile would be
    // too narrow opens the theatre in the same click (twitch.ts).
    const watchFromCard = React.useCallback((handle: string) => {
        const stream = streams.find((candidate) => candidate.handle === handle);
        if (stream) {
            watchOrOpen(stream, 'only');
        } else {
            watchOnly(handle);
        }
    }, [streams]);

    // A relink from the footer's bar that the server accepted. What "me" said
    // about clips of the stream linked before belongs to that stream
    // (addendum 3.5): with another one linked, there is no switch until the
    // server says where this one's is, which "me" read again will.
    const relinked = React.useCallback((streamKey: string) => {
        setMe((current) => {
            if (!current) {
                return current;
            }

            return current.streamKey === streamKey ?
                {...current, streamKey} :
                {...current, streamKey, clipsStreamer: undefined};
        });
        refocusFooter.current = true;
        closeSheet('link');
        refresh();
        loadProfile();
        loadNowPlaying();
        void refreshIdentity();
    }, [refresh, loadProfile, loadNowPlaying, closeSheet]);

    // Somebody let in linking their stream from the footer: they are linked
    // now, and PanelGate mounts the whole panel in place of this one — the
    // same opening as the lock's. Taken into the identity at once, as the
    // lock's Unlock does, and "me" read again behind it.
    const linkedFromLetIn = React.useCallback((streamKey: string) => {
        const base = getIdentity() || me;
        if (base) {
            setIdentity({...base, streamKey, access: 'linked', clipsStreamer: undefined});
        }
        void refreshIdentity();
    }, [me]);

    // Unlink, its own pill in the relink form, on its second press. An empty
    // bar no longer unlinks: now that unlinking locks the whole panel and stops
    // every stream, that would be one stray Enter too many. On success the
    // page follows the identity (access.ts): the lock, saying "You unlinked
    // your stream.", or — for somebody also let in — the watch-only panel.
    const unlink = (event: React.MouseEvent<HTMLButtonElement>) => {
        if (unlinking || !unlinkTwice.press('unlink', event)) {
            return;
        }

        setUnlinking(true);
        setLinkError('');
        noteSelfUnlink();
        unlinkStream().then(() => {
            const base = getIdentity() || me;
            if (base) {
                setIdentity({...base, streamKey: '', access: base.alsoLetIn ? 'letIn' : 'locked', clipsStreamer: undefined});
            }
            void refreshIdentity();
        }).catch((reason: unknown) => {
            noteSelfUnlink(false);
            setLinkError(reason instanceof Error ? reason.message : String(reason));
        }).finally(() => setUnlinking(false));
    };

    // The note edits in place: click the text, type, press Enter. Enter and
    // clicking away are the same act — "I have finished" — so both commit, and
    // only Escape throws the edit away.
    //
    // A ref rather than the savingProfile state to keep the two out of each
    // other's way: Enter commits and then blurs the field, which would commit a
    // second time, and two state updates in one tick would both still see
    // savingProfile as false.
    const noteBusy = React.useRef(false);
    const noteAbandoned = React.useRef(false);
    const noteField = React.useRef<HTMLInputElement>(null);

    // The bar's Save, where the message box has Send.
    const saveRef = React.useRef<HTMLButtonElement | null>(null);

    const commitNote = React.useCallback(() => {
        if (noteBusy.current) {
            return;
        }

        noteBusy.current = true;
        setSavingProfile(true);
        setProfileError('');

        saveMyProfile(draftNote.trim(), true).then((saved) => {
            setProfile(saved);

            // The title is always a field. Saved with the keyboard
            // still in it (Enter, or Save pressed from it), it stays the
            // field being typed in, now holding what was saved; Save, which
            // can go quiet or away, hands its focus back to the field.
            const active = document.activeElement;
            if (active && (active === noteField.current || active === saveRef.current)) {
                setDraftNote(saved.motd || '');
                if (active === saveRef.current) {
                    noteField.current?.focus();
                }
            } else {
                setEditingProfile(false);
            }
            refresh();
        }).catch((reason: unknown) => {
            refocusFooter.current = false;
            setProfileError(reason instanceof Error ? reason.message : String(reason));
        }).finally(() => {
            noteBusy.current = false;
            setSavingProfile(false);
        });
    }, [draftNote, refresh]);

    // The bearer token bar, in its sheet: for somebody linked, opened from
    // the link key in the bar, which is always there (the one home of
    // Relink); for somebody let in, from Link your stream. Closing it — its ×
    // or Cancel, Escape, or a link that saved — puts focus back on what
    // opened it when that is still there, and otherwise on whatever the
    // footer now shows, rather than letting it fall out of the panel.
    const footerRef = React.useRef<HTMLDivElement | null>(null);
    const titleBarRef = React.useRef<HTMLDivElement | null>(null);
    const letInKeyRef = React.useRef<HTMLButtonElement | null>(null);
    const clipsButtonRef = React.useRef<HTMLButtonElement | null>(null);
    const refocusFooter = React.useRef(false);
    const linkFrom = React.useRef<HTMLElement | null>(null);

    const openLink = React.useCallback((from: HTMLElement | null) => {
        setLinkError('');
        linkFrom.current = from;
        setSheet('link');
    }, []);

    const closeLink = React.useCallback(() => {
        refocusFooter.current = true;
        closeSheet('link');
    }, [closeSheet]);

    // "Link your stream" asked for from outside the panel — the clip editor's
    // link action, for somebody let in (lockPending.ts openLock). That leaves
    // a request (identity.ts) and opens the panel, and the panel takes it —
    // when it mounts, or at once if it is already open — and opens its bar,
    // which takes focus. Somebody linked has nothing to do, and the request is
    // let go.
    const linkAsked = useLinkRequest();
    React.useEffect(() => {
        if (linkAsked && takeLinkRequest() && !linked) {
            openLink(letInKeyRef.current);
        }
    }, [linkAsked, linked, openLink]);

    // Only when focus fell out with the form — never when the person has
    // since moved on, to Mattermost's message box or anywhere else, while a
    // save was on its way: taking focus from there would put their next
    // keystrokes into the stream title.
    React.useEffect(() => {
        if (!refocusFooter.current || editingLink || editingProfile) {
            return;
        }

        refocusFooter.current = false;
        const opener = linkFrom.current;
        linkFrom.current = null;
        const active = document.activeElement;
        if (active && active !== document.body && !footerRef.current?.contains(active)) {
            return;
        }

        (usable(opener) ? opener : (titleBarRef.current || letInKeyRef.current || panelRef.current))?.focus();
    });

    // What's on?: its button in the bar opens and closes the picker in its
    // sheet, a fresh one every time, as the calendar does the planning form.
    const onNowRef = React.useRef<HTMLButtonElement | null>(null);
    const onNowFormRef = React.useRef<HTMLDivElement | null>(null);

    // Bumped to put focus back on the chip once the change that moves it has
    // been drawn. A count rather than a flag: an effect left over from the
    // render before would otherwise see the flag first and focus the button
    // that is about to be replaced.
    const [focusOnNow, setFocusOnNow] = React.useState(0);

    const toggleOnNow = () => {
        if (choosingOnNow) {
            closeSheet('onnow');

            return;
        }

        setOnNowKey((key) => key + 1);
        setOnNowError('');
        setSheet('onnow');
    };

    // The picker closing itself — its ×, Escape, or a save. Focus goes back
    // to What's on? in the bar, as the planning form's goes back to the
    // calendar, and only when it was in the picker: a save lands a moment
    // after it is pressed, and by then somebody may be typing a message
    // elsewhere. Put there after the render, once the sheet has gone inert.
    const closeOnNow = React.useCallback(() => {
        const active = document.activeElement;
        const wasInPicker = !active || active === document.body || Boolean(onNowFormRef.current?.contains(active));

        closeSheet('onnow');
        if (wasInPicker) {
            setFocusOnNow((count) => count + 1);
        }
    }, [closeSheet]);

    // Take down, in the picker's sheet. The sheet goes with what it took
    // down, so focus moves to What's on? in the bar — if it was in the sheet
    // to begin with.
    const clearOnNow = React.useCallback(() => {
        setClearingOnNow(true);
        setOnNowError('');

        clearNowPlaying().then(() => {
            const active = document.activeElement;
            const wasInPicker = !active || active === document.body || Boolean(onNowFormRef.current?.contains(active));

            setOnNow(null);
            closeSheet('onnow');
            if (wasInPicker) {
                setFocusOnNow((count) => count + 1);
            }
            refresh();
        }).catch((reason: unknown) => {
            setOnNowError(reason instanceof Error ? reason.message : String(reason));
        }).finally(() => setClearingOnNow(false));
    }, [refresh, closeSheet]);

    React.useEffect(() => {
        if (focusOnNow > 0) {
            onNowRef.current?.focus();
        }
    }, [focusOnNow]);

    // Editing the title is one thing at a time too: whatever sheet was open
    // closes, so the bar is the only thing being changed.
    //
    // The title is always a field, so editing begins when it takes
    // focus (or a key, if it somehow had focus without it), and a draft that
    // was left after a failed save is kept rather than started again.
    const editNote = React.useCallback(() => {
        if (editingProfile) {
            return;
        }
        noteAbandoned.current = false;
        linkFrom.current = null;
        setDraftNote(profile?.motd || '');
        setProfileError('');
        setSheet('');
        setEditingProfile(true);
    }, [profile, editingProfile]);

    // Finished with the field without Enter or Escape — clicked or tabbed
    // away: saved when it says something new, as clicking away always saved;
    // otherwise only the editing ends, with nothing sent.
    const finishNote = React.useCallback(() => {
        if (draftNote.trim() === (profile?.motd || '')) {
            setEditingProfile(false);

            return;
        }

        refocusFooter.current = false;
        commitNote();
    }, [draftNote, profile, commitNote]);

    const noteDirty = editingProfile && draftNote.trim() !== (profile?.motd || '');

    const relist = React.useCallback(() => {
        setSavingProfile(true);
        setProfileError('');

        saveMyProfile(profile?.motd || '', true).then((saved) => {
            setProfile(saved);
            refresh();
        }).catch((reason: unknown) => {
            setProfileError(reason instanceof Error ? reason.message : String(reason));
        }).finally(() => setSavingProfile(false));
    }, [profile, refresh]);

    // The banner, top and (by the Banner placement setting) bottom: the
    // default until the config says otherwise, so it does not flash in.
    const banner = bannerOf(me);

    // What this server calls the plugin, for the region's name, and whether
    // it has an emblem for behind the panel (name.ts).
    const name = useName();
    const artwork = useArtwork();

    // Whether the body has more under the footer than it shows: only then
    // does its foot fade (the stylesheet), so a line cut in half there reads
    // as more to come, and scrolled to the end the last line shows whole.
    // Asked after every render, on every scroll, and whenever the body or
    // anything in it changes size (a picture arriving, a card going).
    const bodyRef = React.useRef<HTMLDivElement | null>(null);
    const [moreBelow, setMoreBelow] = React.useState(false);
    const lookBelow = React.useCallback(() => {
        const body = bodyRef.current;
        setMoreBelow(body !== null && body.scrollTop + body.clientHeight < body.scrollHeight - 2);
    }, []);
    React.useLayoutEffect(lookBelow);
    const sizes = React.useRef<ResizeObserver | null>(null);
    React.useEffect(() => {
        const body = bodyRef.current;
        if (!body || typeof ResizeObserver === 'undefined') {
            return;
        }
        if (!sizes.current) {
            sizes.current = new ResizeObserver(lookBelow);
            sizes.current.observe(body);
        }
        Array.from(body.children).forEach((child) => sizes.current?.observe(child));
    });
    React.useEffect(() => () => {
        sizes.current?.disconnect();
        sizes.current = null;
    }, []);
    // What the two collapsible rows open and close on. The conditions live out
    // here rather than inside the rows, because the thing that animates is the
    // wrapper around them — a row that returns null has no height to animate
    // FROM, so the decision has to be made before it is asked to render.
    const inVoice = Boolean(snapshot.voice?.connected && (snapshot.voice.people || []).length > 0);

    // The ends of streams still held for clipping (snapshot.clipTails), by
    // this browser's clock, while Clips are on.
    const heldEnds = useHeldTails(snapshot.clips?.enabled ? snapshot.clipTails : undefined);
    const liveNow = new Set(streams.map((stream) => stream.handle));

    // The streamer's own say over clips of their stream, in the footer
    // (StreamerClips). Their stream is the one linked to them by hand
    // (spec 3.11); the server decides again when they act. Its handle is
    // remembered once it has been seen, for as long as they stay linked to
    // the same stream, so the end of it can still be wiped for the ten
    // minutes it is held after it stops. A panel that never saw it live —
    // opened only after it stopped, or opened again, which mounts it afresh
    // — finds it among the ends still held, by whose it is, when exactly
    // one that is not live is theirs.
    const ownStream = linked && me?.streamKey ?
        streams.find((stream) => stream.userId === me.userId && stream.linkSource === 'manual' && !stream.alwaysOn) || null : null;
    const ownSeen = React.useRef({key: '', handle: ''});
    React.useEffect(() => {
        if (ownStream && me) {
            ownSeen.current = {key: me.streamKey, handle: ownStream.handle};
        }
    }, [ownStream, me]);
    const ownEnds = linked && me?.streamKey ? heldEnds.filter((tail) => tail.userId === me.userId && !liveNow.has(tail.handle)) : [];
    const ownHandle = ownStream?.handle ||
        (me?.streamKey && ownSeen.current.key === me.streamKey ? ownSeen.current.handle : '') ||
        (ownEnds.length === 1 ? ownEnds[0].handle : '');
    const ownHeldEnd = ownHandle === '' ? undefined : heldEnds.find((tail) => tail.handle === ownHandle && (!tail.userId || tail.userId === me?.userId));
    let ownTail: {until: number; heldMs?: number} | null = null;
    if (typeof ownStream?.clipTail === 'number') {
        ownTail = {until: ownStream.clipTail, heldMs: ownHeldEnd?.heldMs};
    } else if (ownHeldEnd) {
        ownTail = {until: ownHeldEnd.until, heldMs: ownHeldEnd.heldMs};
    }
    const attributed = me && !linked ?
        streams.find((stream) => stream.userId === me.userId && stream.linkSource === 'username' && !stream.alwaysOn) || null : null;
    const clipLength = snapshot.clips?.lengthSeconds || 120;

    // The server's answer to the switch pressed here, until the stream says
    // the same. Switched on, it goes on saying off until the server's next
    // poll of Broadcast Box starts recording it again, which is not news;
    // either way the snapshot may not have caught up yet. Given up after the
    // longest the server can be set to wait between polls.
    const [switched, setSwitched] = React.useState<{at: number; allowed: boolean} | null>(null);
    const ownClip = ownStream?.clip;
    React.useEffect(() => {
        if (switched && ownClip !== undefined && (ownClip === 'off') !== switched.allowed) {
            setSwitched(null);
        }
    }, [switched, ownClip]);
    const justSwitched = switched !== null && Date.now() - switched.at < JUST_SWITCHED_MS;
    const streamer = streamerView({
        clipsEnabled: Boolean(snapshot.clips?.enabled),
        linked: me ? linked : undefined,
        allowed: me?.clipsStreamer?.allowed,
        downloads: me?.clipsStreamer?.downloads,
        lengthSeconds: clipLength,
        own: ownStream,
        ownTail,
        justAllowed: justSwitched && switched?.allowed === true,
        attributed,
        now: Date.now(),
    });

    // What the dot on the scissors in the bar says: 'live' while the server
    // is keeping your stream for clipping this minute (it breathes), 'on'
    // while clips of it are allowed and it is off the air, and 'off'. A stream
    // kept under your username, before you have linked, is kept all the same.
    // With Clips off and only the downloads switch in the sheet, nothing of
    // your stream is kept: 'off'.
    let clipDot: 'live' | 'on' | 'off' = 'off';
    if (streamer?.kind === 'attributed') {
        clipDot = 'live';
    } else if (streamer?.kind === 'streamer' && streamer.allowed) {
        const keeping = ownClip === 'ready' || ownClip === 'starting' || (ownClip === 'off' && justSwitched);
        clipDot = ownStream && keeping ? 'live' : 'on';
    }
    const clipDotWords = {
        live: streamer?.kind === 'attributed' ? 'your stream is being kept for clipping' : 'on, and your stream is being kept for clipping now',
        on: 'on, while you’re live',
        off: 'off',
    }[clipDot];

    // "me" is fetched when the panel opens, the snapshot every poll. Their
    // stream saying off while "me" says allowed, or recorded while "me" says
    // off, outside the moment after a switch here, is the switch moved from
    // another tab or device: "me" is asked for again, once for each time
    // they disagree, so the switch never shows a stale position for long.
    const ownAllowed = me?.clipsStreamer?.allowed;
    const disagrees = ownStream !== null && ownClip !== undefined && ownAllowed !== undefined && !justSwitched &&
        (ownClip === 'off') === ownAllowed;
    const disagreement = disagrees && ownStream ? `${ownStream.handle}|${ownClip}|${String(ownAllowed)}` : '';
    React.useEffect(() => {
        if (disagreement !== '') {
            void refreshIdentity();
        }
    }, [disagreement]);

    // The server's answer to the switch, into "me", which the rest of the
    // page reads it from; and the streams again, so the recording's state —
    // off, or starting again — follows.
    const clipsAllowed = (allowed: boolean) => {
        const base = me || known;
        if (base) {
            setMe({...base, clipsStreamer: {...base.clipsStreamer, allowed}});
        }
        setSwitched({at: Date.now(), allowed});
        refresh();
    };

    // The downloads switch's answer, into "me" the same way. Nothing else
    // follows from it: no recording starts or stops, and the clips' own
    // canDownload is the server's on their next fetch.
    const clipDownloads = (downloads: boolean) => {
        const base = me || known;
        if (base?.clipsStreamer) {
            setMe({...base, clipsStreamer: {...base.clipsStreamer, downloads}});
        }
    };

    // The calendar in the footer's bar: the one way in to planning a stream,
    // there whether or not anything is planned, for somebody linked (nobody
    // else has it). It opens the form in its sheet, and pressing it again
    // closes it.
    const togglePlanning = (event: React.MouseEvent<HTMLButtonElement>) => {
        if (planning) {
            closeSheet('plan');
            setEditing(null);

            return;
        }

        planFrom.current = event.currentTarget;
        setEditing(null);
        setPlanKey((key) => key + 1);
        setSheet('plan');
    };

    // The pencil on one of your plans: the same form, filled in with it, in
    // the same sheet. A fresh key every time, so a form already open on
    // something else is replaced rather than half-kept.
    const editPlan = (item: ScheduleItem, from: HTMLButtonElement) => {
        planFrom.current = from;
        setEditing(item);
        setPlanKey((key) => key + 1);
        setSheet('plan');
    };

    // The form closing itself — its ×, Escape, or a save. The form is about
    // to go inert and then away, and focus inside it would fall out of the
    // panel altogether, so it goes back to what opened it: the calendar, or
    // the pencil on the card being changed while that is still there. Only
    // when it WAS in the form (or had already fallen out): a save finishes a
    // moment after it is pressed, and by then somebody may be typing a
    // message elsewhere.
    const planRef = React.useRef<HTMLDivElement | null>(null);
    const closePlan = React.useCallback(() => {
        const active = document.activeElement;
        const wasInForm = !active || active === document.body || Boolean(planRef.current?.contains(active));

        closeSheet('plan');
        setEditing(null);
        if (wasInForm) {
            const opener = planFrom.current;
            (usable(opener) ? opener : planButtonRef.current)?.focus();
        }
    }, [closeSheet]);

    // The clip controls' sheet, from the scissors in the bar. Focus goes into
    // it as it opens — onto the switch, which is what it is for — and back to
    // the scissors when it closes from inside.
    const clipsOpen = sheet === 'clips';
    const clipsSheetRef = React.useRef<HTMLDivElement | null>(null);
    const toggleClips = () => {
        if (clipsOpen) {
            closeSheet('clips');
        } else {
            setSheet('clips');
        }
    };
    const closeClips = React.useCallback(() => {
        const active = document.activeElement;
        const wasInSheet = !active || active === document.body || Boolean(clipsSheetRef.current?.contains(active));

        closeSheet('clips');
        if (wasInSheet) {
            clipsButtonRef.current?.focus();
        }
    }, [closeSheet]);

    React.useEffect(() => {
        if (clipsOpen) {
            clipsSheetRef.current?.querySelector<HTMLElement>('[role="switch"]')?.focus({preventScroll: true});
        }
    }, [clipsOpen]);

    // Nothing left to show in it — Clips switched off with no downloads to
    // switch either, or the link changed — and it closes rather than staying
    // open on nothing. Focus that was in it is the footer's to catch
    // (footerFocus, below). Clips switched off while an admin allows
    // downloads leaves the downloads switch, and the sheet stays open on it.
    const hasStreamer = streamer !== null;
    React.useEffect(() => {
        if (!hasStreamer) {
            closeSheet('clips');
        }
    }, [hasStreamer, closeSheet]);

    // A press anywhere outside the footer closes it, as a popover does. Only
    // this sheet: the others hold something half typed, which a stray click
    // should not throw away.
    React.useEffect(() => {
        if (!clipsOpen) {
            return undefined;
        }

        const away = (event: PointerEvent) => {
            if (!footerRef.current?.contains(event.target as Node)) {
                closeSheet('clips');
            }
        };
        document.addEventListener('pointerdown', away, true);

        return () => document.removeEventListener('pointerdown', away, true);
    }, [clipsOpen, closeSheet]);

    // Connected accounts (design/3.4.0.md 5.2-5.3, Victor's P1 pick B, "Your
    // stream and accounts"): the relink key, which everybody linked always
    // has, opens their stream (the token bar, Save and Unlink) and then a row
    // for each of Twitch, Steam and Hytale they may connect or have
    // connected (AccountSheet.tsx). The Twitch tool has left the bar, so the
    // bar has five tools at most and adds none. Linked only: connecting is,
    // and nobody else has the relink key.
    const accountsView = useAccountsFor(access);
    const ownSteam = me ? accountsView?.people[me.userId]?.steam : undefined;
    const accountRows = accountRowsFor(me, access);

    // Playing now (design/3.4.0.md 5.4, Victor's P2 pick B): who is in a
    // Steam game, by their Mattermost name, for the row under In voice;
    // nothing at all for a reader who may not watch, who has no panel here
    // anyway.
    const playing = playingNow(accountsView, access, personName);

    // A row's Connect turns the sheet into that service's flow, one at a
    // time, with an arrow back to the list.
    const [accountFlow, setAccountFlow] = React.useState<AnyService | ''>('');
    const accountFlowRef = React.useRef(accountFlow);
    accountFlowRef.current = accountFlow;

    // A Disconnect's words keep the place they are said in for their few
    // seconds, from its second press: the list and the sheet's head, even
    // when "me" no longer gives that row anything to show (a service
    // switched off here, with nothing connected now), as the bar once kept
    // the Twitch tool and its sheet. Let go when the words end, or the sheet
    // closes.
    const [accountsHeld, setAccountsHeld] = React.useState(false);
    const holdAccounts = React.useCallback((service: AnyService | '') => setAccountsHeld(service !== ''), []);
    const holdForTwitch = React.useCallback(() => setAccountsHeld(true), []);
    React.useEffect(() => {
        if (!editingLink) {
            setAccountFlow('');
            setAccountsHeld(false);
        }
    }, [editingLink]);

    // Whether the sheet is "Your stream and accounts" or, with no account
    // rows at all (every service off, or blocked), exactly today's "Relink
    // your stream".
    const relinkWithRows = accountRows.length > 0 || (editingLink && accountsHeld);

    // Which of the two it opened as, decided once, as it opens: with account
    // rows, focus goes to the sheet's words, so somebody who came to connect
    // Steam is not dropped into the bearer-token box, which is one Tab away;
    // without, into the token field, as before. Rows coming or going later
    // never move focus into the field.
    const relinkOpenedWithRows = React.useRef(relinkWithRows);
    if (!editingLink) {
        relinkOpenedWithRows.current = relinkWithRows;
    }
    const linkHeadingRef = React.useRef<HTMLSpanElement | null>(null);
    React.useEffect(() => {
        if (editingLink && relinkOpenedWithRows.current) {
            linkHeadingRef.current?.focus({preventScroll: true});
        }
    }, [editingLink]);

    // The list gone from under focus (its last row's words said): focus on
    // the sheet's words, never on the page's body.
    const relinkHadRows = React.useRef(relinkWithRows);
    React.useLayoutEffect(() => {
        const had = relinkHadRows.current;
        relinkHadRows.current = relinkWithRows;
        if (!had || relinkWithRows || !editingLink || accountFlow !== '') {
            return;
        }

        const active = document.activeElement;
        if (!active || active === document.body) {
            linkHeadingRef.current?.focus({preventScroll: true});
        }
    }, [relinkWithRows, editingLink, accountFlow]);

    // Back from a flow (its arrow, Cancel, Escape, or its words after a
    // Disconnect running out): the list, with focus on the Connect of the
    // row it came from, or else on the sheet's words. Only when focus was
    // in the sheet, or fell out of it with the flow.
    const backFrom = React.useRef<AnyService | ''>('');
    const flowBack = React.useCallback(() => {
        // A second call before the list is back keeps the first one's row.
        if (accountFlowRef.current !== '') {
            backFrom.current = accountFlowRef.current;
        }
        setAccountFlow('');
        setAccountsHeld(false);
    }, []);

    // A flow whose row has left the list goes with it, back to the list: an
    // admin blocked the reader from that service, or switched it off with
    // nothing connected. Continue is never offered for a service the reader
    // has no row for, as 3.3.0's Twitch sheet closed when its tool went. Not
    // while a Disconnect's words hold the place for their few seconds.
    const accountRowsKey = accountRows.join(' ');
    React.useEffect(() => {
        if (accountFlow !== '' && !accountsHeld && !accountRowsKey.split(' ').includes(accountFlow)) {
            flowBack();
        }
    }, [accountFlow, accountsHeld, accountRowsKey, flowBack]);
    React.useEffect(() => {
        if (accountFlow !== '' || backFrom.current === '') {
            return;
        }

        const from = backFrom.current;
        backFrom.current = '';
        const sheetElement = document.getElementById(footerIds.link);
        const active = document.activeElement;
        if (active && active !== document.body && !sheetElement?.contains(active)) {
            return;
        }

        const connect = sheetElement?.querySelector<HTMLElement>(`.vntv-account-act[data-service="${from}"]`);
        (connect || linkHeadingRef.current)?.focus({preventScroll: true});
    }, [accountFlow, footerIds.link]);

    // The soundboard (design/3.2.0.md 7.13, Victor's P1 pick: a sixth tool in
    // the bar). There only for somebody linked, while the soundboard is on and
    // Mumble connected (the snapshot carries it then), and while the voice
    // roster names the reader. Its sheet asks for the board again when the
    // board's version moves or the reader's own row in the roster does.
    const voiceMe = me ? voiceOf(snapshot, me.userId) : null;
    const soundsTool = linked && Boolean(snapshot.soundboard) && Boolean(voiceMe);
    const soundsOpen = sheet === 'sounds';
    const [soundsKey, setSoundsKey] = React.useState(0);
    const soundsButtonRef = React.useRef<HTMLButtonElement | null>(null);
    // The reader's own rows in the roster, as the sheet's reason to ask for
    // the board again. How many of them there are is part of it: a second
    // Mumble session (a phone beside the desktop, through Names that differ)
    // turns the tiles off with "You're in Mumble twice", and leaving it turns
    // them on again, though the first row never changed.
    const voiceSessions = voiceMe ? (snapshot.voice?.people || []).filter((person) => person.userId === me?.userId).length : 0;
    const rosterKey = voiceMe ? `${voiceMe.name}|${voiceMe.muted ? 'muted' : ''}|${voiceSessions}` : '';
    const toggleSounds = () => {
        if (soundsOpen) {
            closeSheet('sounds');

            return;
        }

        setSoundsKey((key) => key + 1);
        setSheet('sounds');
    };

    // Its ×, Escape: focus back on the tool, when it was in the sheet; or,
    // when the tool has gone (the reader left Mumble), on the bar's title.
    const closeSounds = React.useCallback(() => {
        const active = document.activeElement;
        const inSheet = document.getElementById(footerIds.sounds);
        const wasInSheet = !active || active === document.body || Boolean(inSheet?.contains(active));

        closeSheet('sounds');
        if (wasInSheet) {
            (soundsButtonRef.current || titleBarRef.current || panelRef.current)?.focus();
        }
    }, [closeSheet, footerIds.sounds]);

    // The tool gone while its sheet is open — Mumble out of reach, or the
    // reader gone from it: the sheet stays to say why, and closes when the
    // person next leaves it (a press outside the footer, or focus going out
    // of it).
    React.useEffect(() => {
        if (soundsTool || !soundsOpen) {
            return undefined;
        }

        const away = (event: Event) => {
            const target = (event as FocusEvent).relatedTarget ?? event.target;
            if (event.type === 'focusout' && (event as FocusEvent).relatedTarget === null) {
                return;
            }
            if (!footerRef.current?.contains(target as Node | null)) {
                closeSheet('sounds');
            }
        };
        const footer = footerRef.current;
        document.addEventListener('pointerdown', away, true);
        footer?.addEventListener('focusout', away);

        return () => {
            document.removeEventListener('pointerdown', away, true);
            footer?.removeEventListener('focusout', away);
        };
    }, [soundsTool, soundsOpen, closeSheet]);

    // Built once and placed in one of two positions, so the rail itself does
    // not have to know where it is. The whole category moves as one.
    const upNext = (
        <div
            className='vntv-upnext-area'
            {...trackRowFocus}
        >
            <UpNext
                items={snapshot.schedule || []}
                liveHandles={liveHandles}
                meUserId={me?.userId}
                isAdmin={me?.isAdmin}
                onChanged={refresh}
                onEdit={editPlan}
                canJoin={canPlan}
                canPlan={canPlan}
                canShare={canPlan}
            />
        </div>
    );

    // Focus on a tile, when what had it goes: the tile swapped for the one
    // that waits for its stream when it drops, and back when it returns, or
    // given up after waiting. Each tile looks after its own buttons
    // (Player.tsx); this is for the tile itself going, when focus goes to
    // the tile now in its place, or else to the panel — not to the page's
    // body, from where the next letter would go to Mattermost's message box
    // just when somebody watching wants to press C (focusHome.ts).
    const tileFocused = React.useRef('');
    const stageFocus = useFocusHome(() => {
        const handle = tileFocused.current;
        const tile = handle === '' ? null :
            gridRef.current?.querySelector<HTMLElement>(`.vntv-tile[data-handle="${CSS.escape(handle)}"] > .vntv-player`);

        return tile || panelRef.current;
    });

    // The same for the streamer's controls going all at once, while one of
    // them has focus — Clips switched off, or a link changed: to the title,
    // or the panel. Only those: everything else in the footer puts focus
    // back itself.
    const footerFocus = useFocusHome(
        () => titleBarRef.current || letInKeyRef.current || panelRef.current,
        (target) => target.closest('.vntv-clips-streamer') !== null,
    );

    const unlinkWords = (() => {
        if (unlinking) {
            return 'Unlinking…';
        }
        if (unlinkTwice.armed === 'unlink') {
            return me?.alsoLetIn ?
                'Unlink? You will be able to watch only, until you enter your bearer token again.' :
                `Unlink? ${name} locks for you until you enter your bearer token again.`;
        }

        return 'Unlink';
    })();

    return (
        <div
            ref={(element) => {
                panelRef.current = element;
                onRoot?.(element);
            }}
            className='vntv vntv-panel'
            style={banner.style}
            data-boot={booting ? 'true' : undefined}
            data-emblem={artwork.emblem ? 'true' : undefined}

            // Focusable, but not a tab stop: where focus is put after the panel
            // comes back by itself after a reply (away.ts), one Tab from Back
            // to thread. Named, so that move is read out as arriving in
            // the panel rather than in an unnamed group.
            tabIndex={-1}
            role='region'
            aria-label={name}

            // Stray keys there do nothing. Mattermost listens on the document
            // and sends any printable key, or a paste, pressed outside a text
            // box to the channel's message box (use_textbox_focus.tsx and
            // shouldFocusMainTextbox in post_utils.ts), where the next Enter
            // would post a reply typed on after the thread had gone into the
            // channel. So those keys stop here while the panel itself has
            // focus; React handles them at Mattermost's root, below the
            // document. Tab, F6 and shortcuts with a modifier go on as usual.
            // A paste is one by Mattermost's own rule (focusTrap.ts), which
            // knows a Ctrl+V from a keyboard that types another letter there.
            onKeyDown={(event) => {
                if (event.target !== event.currentTarget) {
                    return;
                }

                const command = event.ctrlKey || event.metaKey;
                const paste = isPasteKey(event);
                if (paste || (event.key.length === 1 && !command && !event.altKey)) {
                    event.stopPropagation();
                }
            }}
        >
            {banner.text !== '' && <div className='vntv-banner'>{banner.text}</div>}

            <div
                ref={bodyRef}
                className='vntv-body'
                data-more={moreBelow ? 'true' : undefined}
                onScroll={lookBelow}
            >
                <ReturnStrip/>

                {/* The store did not answer, or "me" could not be read, after
                    a definite answer: nothing is taken away for that, and it
                    is said here, quietly, until an answer comes. */}
                {accessUnchecked ? (
                    <div className='vntv-notice vntv-notice-quiet'>
                        <span>{`${name} could not check your access just now.`}</span>
                        <TryAgain/>
                    </div>
                ) : null}

                {/* With Twitch on, a server can have streams without Broadcast
                    Box at all, and only an admin needs telling. */}
                {!snapshot.configured && (!snapshot.twitchOn || me?.isAdmin) && (
                    <div className='vntv-notice vntv-notice-error'>
                        {'Broadcast Box is not set up yet. A system admin can add the server URL in '}
                        <b>{consolePath('Broadcast Box')}</b>{'.'}
                    </div>
                )}

                {snapshot.configured && snapshot.error && (
                    <div className='vntv-notice vntv-notice-error'>{snapshot.error}</div>
                )}

                <AdminAlerts me={me}/>

                <Reveal
                    show={inVoice}
                    label='In voice'
                >
                    <VoiceStrip
                        people={snapshot.voice?.people}
                        connected={snapshot.voice?.connected}
                    />
                </Reveal>

                {/* Directly under In voice, its twin, and like it nothing at
                    all while nobody is in a game. */}
                <Reveal
                    show={playing.length > 0}
                    label='Playing'
                    className='vntv-reveal-playing'
                >
                    <PlayingStrip items={playing}/>
                </Reveal>

                {/* Up next sits UNDER the set while there is one thing to
                    watch, where it reads as "and after this". With two or more
                    tiles the set is tall enough that anything under it is
                    below the fold, so it goes back to the top — under the voice
                    strip, which is the row about right now.

                    Two placements rather than one moved by CSS order: they are
                    different positions in the reading order, and a screen reader
                    follows the DOM. */}
                {upNextPlace === 'above' && upNext}

                <div
                    className='vntv-stage'
                    ref={stageRef}
                    onFocus={(event) => {
                        // A Twitch tile's theatre is a portal, but its focus
                        // comes here through React all the same; it carries
                        // its tile's handle, so closing with its stream puts
                        // focus on whatever took that tile's place.
                        tileFocused.current = (event.target as Element).closest<HTMLElement>('.vntv-tile, .vntv-twitch-theatre')?.dataset.handle || '';
                        stageFocus.onFocus(event);
                    }}
                    onBlur={stageFocus.onBlur}
                >
                    {tiles.length > 0 ? (
                        <div
                            className='vntv-grid'
                            ref={gridRef}
                            data-count={Math.max(1, placedTiles.length)}
                            data-leaving={placedTiles.length < tiles.length ? 'true' : undefined}
                        >
                            {tiles.map(({handle, title, seen, stream}) => {
                                const state = closing[handle];
                                const at = state === 'leaving' ? leavingAt[handle] : undefined;

                                // Twitch's: a different player, a frame of
                                // Twitch's own (TwitchPlayer.tsx), and a tile
                                // that always spans the whole row, after the
                                // Broadcast Box ones — by the stylesheet's
                                // order, never by moving it in the page,
                                // which would reload its frame.
                                const twitchTile = stream ? stream.source === 'twitch' : isTwitchHandle(handle);

                                // A wrapper per tile, so the panel can lift one
                                // out of the grid and glide the others without
                                // either player knowing it is happening.
                                return (
                                    <div
                                        key={handle}
                                        className='vntv-tile'
                                        data-handle={handle}
                                        data-source={twitchTile ? 'twitch' : undefined}
                                        data-leaving={at ? 'true' : undefined}
                                        style={at ? {top: at.y, left: at.x, width: at.w, height: at.h} : undefined}
                                    >
                                        {stream && stream.source === 'twitch' ? (
                                            <TwitchPlayer
                                                stream={stream}
                                                audible={audio === handle}
                                                onAudio={setAudio}
                                                onClose={() => closeTile(handle)}
                                                closing={Boolean(state)}
                                            />
                                        ) : null}
                                        {stream && stream.source !== 'twitch' ? (
                                            <Player
                                                stream={stream}
                                                audible={audio === handle}
                                                onAudio={setAudio}
                                                onClose={() => closeTile(handle)}
                                                closing={Boolean(state)}
                                            />
                                        ) : null}
                                        {stream ? null : (
                                            <WaitingTile
                                                handle={handle}
                                                title={title}
                                                seen={seen}
                                                onClose={() => closeTile(handle)}
                                                closing={Boolean(state)}
                                            />
                                        )}
                                    </div>
                                );
                            })}
                        </div>
                    ) : (

                        // A television that is on and showing nothing, rather
                        // than a gap where one goes. It keeps the player's own
                        // chrome so the panel does not change shape when a
                        // stream finally arrives.
                        //
                        // No note at all: "no signal" says it, the list of live
                        // streams directly underneath says what to do about it,
                        // and a television does not explain itself.
                        //
                        // It warms up only when it is there as the panel opens;
                        // arriving because a stream was just switched off, it
                        // fades in instead.
                        <NoSignalTile
                            powerOn={booting}
                            ghost={me?.noSignalGhost}
                        />
                    )}
                </div>

                {upNextPlace === 'below' && upNext}

                {cards.length > 0 && (
                    <div
                        className='vntv-live-section'
                        ref={liveRef}
                    >
                        <div className='vntv-live-head'>
                            <p
                                id='villanet-live-now'
                                className='vntv-live-eyebrow'
                            >
                                {'Live now'}
                                <span className='vntv-live-count'>{cards.length}</span>
                            </p>
                        </div>
                        <ul
                            className='vntv-live-grid'
                            aria-labelledby='villanet-live-now'
                        >
                            {cards.map((stream) => (
                                <LiveCard
                                    key={stream.handle}
                                    stream={stream}
                                    voice={voiceOf(snapshot, stream.userId)}
                                    watching={watching.includes(stream.handle)}
                                    full={watching.length >= MAX_WATCHING}
                                    anyWatching={watching.length > 0}
                                    manyWatching={watching.length > 1}
                                    previews={snapshot.livePreviews === true}
                                    onWatchOnly={watchFromCard}
                                    onToggle={(handle) => (watching.includes(handle) ? closeTile(handle) : watchOrOpen(stream, 'toggle'))}
                                />
                            ))}
                        </ul>
                    </div>
                )}

            </div>

            <div
                ref={footerRef}
                className='vntv-footer'

                // The 24px strip under the bar is the bottom
                // banner when there is one.
                data-strip={banner.atBottom ? 'banner' : undefined}
                onFocus={footerFocus.onFocus}
                onBlur={footerFocus.onBlur}
            >
                {/* Broadcast Box has the stream hidden from its list: said
                    over the bar, on the same glass, with the one thing that
                    puts it right. */}
                {profile?.available && !profile.isPublic && !editingProfile && (
                    <div className='vntv-bar-alert'>
                        <span className='vntv-bar-alert-text'>
                            {'Broadcast Box has your stream hidden from the list, so nobody here sees you go live.'}
                        </span>
                        <button
                            type='button'
                            className='vntv-sheet-action'
                            disabled={savingProfile}
                            onClick={relist}
                        >{savingProfile ? 'Saving…' : 'Show me'}</button>
                    </div>
                )}

                {/* A Twitch, Steam or Hytale account they had connected was
                    proved by somebody else and moved to them: said over the
                    bar until they have read it, one at a time, the newest
                    first (design/twitch-live.md 4.5, design/3.4.0.md 5.6). */}
                {me && linked ? <AccountNotices me={me}/> : null}

                {/* The title could not be saved: said over the bar,
                    as its other warnings are. Nothing but the strip is ever
                    under the bar; Saving is said by Save itself. */}
                {profileError !== '' ? (
                    <div
                        className='vntv-bar-alert'
                        role='alert'
                    >
                        <span className='vntv-bar-alert-text'>{profileError}</span>
                    </div>
                ) : null}

                {/* The dock: one quiet bar, and the sheets that rise out of it.

                    Linked, the bar is the stream title — click it to change it
                    — with a few small tools at its end: What's on?, the
                    scissors for clips of your stream, whose dot says whether
                    they are on, the link key that relinks your stream (always
                    there: its one home, whatever else is switched on, and
                    since 3.4.0 the home of connected accounts too), and the
                    calendar that plans a stream. Nothing in the bar explains
                    itself in a paragraph: each tool says what it is when
                    pointed at, and what it opens says the rest.

                    Let in to watch only, it is one quiet line, WATCH ONLY,
                    with the key that links a stream — nothing else prompts or
                    explains (design/lock-screen.md 5).

                    Each tool opens a sheet, one at a time. The sheets come
                    after the bar in the page, so the keyboard goes from a tool
                    into what it opened, and the stylesheet draws them above
                    it: the footer sits on the bottom of the panel, so it grows
                    upward and the bar never moves under the pointer that
                    pressed it. */}
                {me && linked ? (
                    <div className='vntv-dock'>
                        {/* A new achievement step, over the bar for a few
                            seconds; in the earner's own panel only
                            (design/3.2.0.md 5.4). */}
                        <AchievementToast userId={me.userId}/>

                        <div
                            ref={titleBarRef}
                            className='vntv-bar'
                            role='group'
                            aria-label='Your stream'

                            // Where Escape from the title puts
                            // focus, and the footer's home for it: not a
                            // tab stop.
                            tabIndex={-1}
                        >
                            {/* The stream title is the message
                                box's twin: a real one-line field, always a
                                field, holding the saved title. Enter saves,
                                Escape puts the saved title back and leaves
                                the field, and clicking or tabbing away saves
                                what changed, as it always has. */}
                            {profile?.available ? (
                                <input
                                    ref={noteField}
                                    className='vntv-bar-input'
                                    type='text'
                                    aria-label='Stream title'
                                    title='The title everybody sees on your stream.'
                                    value={editingProfile ? draftNote : (profile.motd || '')}
                                    maxLength={200}
                                    placeholder='What are you streaming?'
                                    autoComplete='off'
                                    onFocus={editNote}
                                    onChange={(event) => {
                                        if (!editingProfile) {
                                            editNote();
                                        }
                                        setDraftNote(event.target.value);
                                    }}
                                    onBlur={(event) => {
                                        if (noteAbandoned.current) {
                                            noteAbandoned.current = false;
                                            return;
                                        }

                                        // On to Save, to press it: not
                                        // finished yet.
                                        if (event.relatedTarget && event.relatedTarget === saveRef.current) {
                                            return;
                                        }

                                        finishNote();
                                    }}
                                    onKeyDown={(event) => {
                                        if (event.key === 'Enter') {
                                            event.preventDefault();
                                            event.stopPropagation();
                                            if (noteDirty) {
                                                refocusFooter.current = false;
                                                commitNote();
                                            }
                                        }
                                        if (event.key === 'Escape') {
                                            event.preventDefault();
                                            event.stopPropagation();
                                            refocusFooter.current = false;
                                            noteAbandoned.current = true;
                                            setProfileError('');
                                            setEditingProfile(false);
                                            titleBarRef.current?.focus();
                                        }
                                    }}
                                />
                            ) : null}
                            {profile === null ? <span className='vntv-bar-loading'/> : null}

                            {/* The title cannot be set — the link has stopped
                                working, say, after a token was rotated — and
                                relinking is what fixes it: said here, and the
                                link key beside it lights up. */}
                            {profile && !profile.available ? (
                                <span className='vntv-bar-problem'>
                                    <span className='vntv-bar-problem-text'>
                                        {profile.message || 'Your stream title cannot be set right now.'}
                                    </span>
                                </span>
                            ) : null}

                            <span className='vntv-bar-tools'>
                                {/* The soundboard: the first tool, while the
                                    reader is in voice. */}
                                {soundsTool ? (
                                    <SoundboardTool
                                        open={soundsOpen}
                                        muted={Boolean(voiceMe?.muted)}
                                        sheetId={footerIds.sounds}
                                        onToggle={toggleSounds}
                                        buttonRef={soundsButtonRef}
                                    />
                                ) : null}

                                {/* Shown whether or not the title can be set:
                                    a stream Broadcast Box keeps no profile for
                                    has no title to write, and this is then the
                                    only way to say what is on it. Once it is
                                    known, so it does not flash empty before
                                    what was said. */}
                                {onNow !== undefined ? (
                                    <WhatsOnChip
                                        nowPlaying={onNow}
                                        open={choosingOnNow}
                                        pickerId={footerIds.onNow}
                                        onOpen={toggleOnNow}
                                        buttonRef={onNowRef}
                                    />
                                ) : null}

                                {/* Clips of your stream: the scissors, with a
                                    dot that says whether they are on — red,
                                    and breathing while your stream is being
                                    kept — opening the switch, Wipe and the
                                    rest in a sheet. Its name stays the same
                                    whatever the dot says; what the dot says
                                    is its description. */}
                                {streamer ? (
                                    <button
                                        ref={clipsButtonRef}
                                        type='button'
                                        className='vntv-bar-tool vntv-bar-clips'
                                        data-state={clipDot}
                                        aria-label='Clips of your stream'
                                        aria-expanded={clipsOpen}
                                        aria-controls={clipsOpen ? footerIds.clips : undefined}
                                        aria-describedby={footerIds.clipState}
                                        title={`Clips of your stream: ${clipDotWords}`}
                                        onClick={toggleClips}
                                    >
                                        <ScissorsIcon size={17}/>
                                        <span
                                            className='vntv-bar-dot'
                                            aria-hidden={true}
                                        />
                                        <span
                                            id={footerIds.clipState}
                                            className='vntv-sr-only'
                                        >{clipDotWords}</span>
                                    </button>
                                ) : null}

                                {/* Relink: one home, always here, whether or
                                    not Clips are on — the bearer token bar in
                                    its sheet, with Unlink beside it, and the
                                    reader's connected accounts under them.
                                    Lit while the stream title cannot be set,
                                    which relinking is what fixes. */}
                                <button
                                    type='button'
                                    className='vntv-bar-tool vntv-bar-relinktool'
                                    data-attention={profile && !profile.available ? 'true' : undefined}
                                    aria-label={relinkWithRows ? relinkKeyWithAccounts(me.streamKey) : `Relink your stream, linked as ${me.streamKey}`}
                                    aria-expanded={editingLink}
                                    aria-controls={editingLink ? footerIds.link : undefined}
                                    title={relinkWithRows ?
                                        relinkKeyWithAccounts(me.streamKey) :
                                        `Linked as ${me.streamKey}${me.linkedLive ? ' · live now' : ''}. Click to relink.`}
                                    onClick={(event) => (editingLink ? closeLink() : openLink(event.currentTarget))}
                                ><LinkIcon size={16}/></button>

                                <button
                                    ref={planButtonRef}
                                    type='button'
                                    className='vntv-bar-tool'
                                    aria-label='Plan a stream'
                                    aria-expanded={planning}
                                    aria-controls={planning ? footerIds.plan : undefined}
                                    title='Plan a stream'
                                    onClick={togglePlanning}
                                ><CalendarPlusIcon size={17}/></button>
                            </span>

                            {/* Save, where the message box has
                                Send: quiet until the field says something
                                new, as Send is until there is a message.
                                Never disabled, which would drop the
                                keyboard; pressed with the pointer it leaves
                                focus in the field. */}
                            {profile?.available ? (
                                <button
                                    ref={saveRef}
                                    type='button'
                                    className='vntv-bar-save'
                                    data-dirty={noteDirty || savingProfile ? 'true' : undefined}
                                    data-saving={savingProfile ? 'true' : undefined}
                                    aria-disabled={noteDirty && !savingProfile ? undefined : true}
                                    aria-label={savingProfile ? 'Saving the stream title' : 'Save the stream title'}
                                    title={savingProfile ? 'Saving…' : 'Save the stream title'}
                                    onMouseDown={(event) => event.preventDefault()}
                                    onBlur={(event) => {
                                        if (event.relatedTarget && event.relatedTarget === noteField.current) {
                                            return;
                                        }
                                        if (editingProfile && !savingProfile) {
                                            finishNote();
                                        }
                                    }}
                                    onClick={() => {
                                        if (noteDirty && !savingProfile) {
                                            refocusFooter.current = false;
                                            commitNote();
                                        }
                                    }}
                                >
                                    {savingProfile ? <span className='vntv-bar-save-words'>{'Saving'}</span> : <CheckIcon size={18}/>}
                                </button>
                            ) : null}
                        </div>

                        {linked ? (
                            <Reveal
                                show={choosingOnNow}
                                label='What’s on right now'
                            >
                                <div
                                    ref={onNowFormRef}
                                    id={footerIds.onNow}
                                    className='vntv-sheet'
                                    role='group'
                                    aria-label='What’s on right now'
                                >
                                    <SheetHead
                                        title='What’s on right now'
                                        note='Clears itself after your stream ends'
                                        onClose={closeOnNow}
                                    >
                                        {/* Not disabled while it is on its way:
                                            a disabled button drops the keyboard
                                            focus it had. */}
                                        {onNow ? (
                                            <button
                                                type='button'
                                                className='vntv-sheet-action'
                                                aria-label={`Take down what’s on: ${onNow.title}`}
                                                aria-disabled={clearingOnNow ? true : undefined}
                                                onClick={() => {
                                                    if (!clearingOnNow) {
                                                        clearOnNow();
                                                    }
                                                }}
                                            >{clearingOnNow ? 'Taking down…' : 'Take down'}</button>
                                        ) : null}
                                    </SheetHead>
                                    <WhatsOnPicker
                                        key={onNowKey}
                                        nowPlaying={onNow || null}
                                        onSaved={(next) => {
                                            setOnNow(next);
                                            refresh();
                                        }}
                                        onDone={closeOnNow}
                                    />
                                    {onNowError !== '' && <span className='vntv-hint vntv-notice-error'>{onNowError}</span>}
                                </div>
                            </Reveal>
                        ) : null}

                        {canPlan ? (
                            <Reveal
                                show={planning}
                                label={editing ? 'Change a plan' : 'Plan a stream'}
                            >
                                <div
                                    ref={planRef}
                                    id={footerIds.plan}
                                    className='vntv-sheet vntv-sheet-plan'
                                    role='group'
                                    aria-label={editing ? `Change ${editing.title}` : 'Plan a stream'}
                                >
                                    <SheetHead
                                        title={editing ? 'Change a plan' : 'Plan a stream'}
                                        note={editing ? editing.title : 'It shows in Up next for everybody'}
                                        onClose={closePlan}
                                    />
                                    <ScheduleControls
                                        key={planKey}
                                        plan={editing}
                                        onChanged={refresh}
                                        onDone={closePlan}
                                    />
                                </div>
                            </Reveal>
                        ) : null}

                        {/* Kept in the page while it is closed, inert: a
                            switch pressed just before it closes still has its
                            answer taken into "me" when the server gives it. */}
                        {streamer ? (
                            <Reveal
                                show={clipsOpen}
                                label='Clips of your stream'
                                keep={true}
                            >
                                <div
                                    ref={clipsSheetRef}
                                    id={footerIds.clips}
                                    className='vntv-sheet'
                                    onKeyDown={(event) => {
                                        if (event.key !== 'Escape' || composing(event)) {
                                            return;
                                        }

                                        event.preventDefault();
                                        event.stopPropagation();
                                        closeClips();
                                    }}
                                >
                                    <SheetHead
                                        title='Clips of your stream'
                                        onClose={closeClips}
                                    />
                                    <StreamerClips
                                        view={streamer}
                                        lengthSeconds={clipLength}
                                        handle={me.streamKey ? ownHandle : ''}
                                        onAllowed={clipsAllowed}
                                        onWiped={refresh}
                                        onDownloads={clipDownloads}
                                    />
                                </div>
                            </Reveal>
                        ) : null}

                        <Reveal
                            show={editingLink}
                            label={relinkWithRows ? SHEET_HEAD_WITH_ACCOUNTS : SHEET_HEAD_WITHOUT}
                        >
                            <div
                                id={footerIds.link}
                                className='vntv-sheet'
                                role='group'
                                aria-label={relinkWithRows ? SHEET_HEAD_WITH_ACCOUNTS : SHEET_HEAD_WITHOUT}

                                // Escape from anywhere in it closes it, as
                                // every footer sheet does; in the bar, an
                                // error is cleared first, and in a flow it
                                // goes back to the list instead. A paste or
                                // a letter pressed on Save, Show or Unlink
                                // goes into the bar, never to Mattermost's
                                // message box (focusTrap.ts); one pressed in
                                // the list or a flow stays there.
                                onKeyDown={(event) => linkSheetKeys(event, linkBarRef.current, closeLink)}
                            >
                                {accountFlow === 'twitch' ? (
                                    <TwitchFlow
                                        me={me}
                                        sheetId={footerIds.link}
                                        onBack={flowBack}
                                        onClose={closeLink}
                                        onDisconnecting={holdForTwitch}
                                    />
                                ) : null}
                                {accountFlow === 'steam' || accountFlow === 'hytale' ? (
                                    <AccountFlow
                                        key={accountFlow}
                                        service={accountFlow}
                                        me={me}
                                        steam={ownSteam}
                                        onBack={flowBack}
                                        onClose={closeLink}
                                        onSaying={holdAccounts}
                                    />
                                ) : null}
                                {accountFlow === '' ? (
                                    <>
                                        <SheetHead
                                            title={relinkWithRows ? SHEET_HEAD_WITH_ACCOUNTS : SHEET_HEAD_WITHOUT}
                                            note={`Linked as ${me.streamKey}${me.linkedLive ? ' · live now' : ''}`}
                                            onClose={closeLink}
                                            headingRef={linkHeadingRef}
                                        />
                                        <TokenBar
                                            variant='relink'
                                            barRef={linkBarRef}
                                            focusOnMount={!relinkOpenedWithRows.current}
                                            onLinked={relinked}
                                            onEscape={closeLink}
                                        >
                                            <button
                                                type='button'
                                                className='vntv-sheet-action vntv-unlink'
                                                data-armed={unlinkTwice.armed === 'unlink' ? 'true' : undefined}
                                                aria-disabled={unlinking ? true : undefined}
                                                onClick={unlink}
                                            >{unlinkWords}</button>
                                        </TokenBar>

                                        {/* What Unlink's first press changed it
                                            to, read out: heard, as Wipe's is. */}
                                        <p
                                            className='vntv-sr-only'
                                            role='status'
                                        >{unlinkTwice.armed === 'unlink' && !unlinking ? unlinkWords : ''}</p>
                                        {linkError !== '' && <span className='vntv-hint vntv-notice-error'>{linkError}</span>}

                                        {/* Connected accounts, under the stream
                                            (design/3.4.0.md 5.2). */}
                                        {relinkWithRows ? (
                                            <AccountsList
                                                me={me}
                                                services={accountRows}
                                                steam={ownSteam}
                                                onConnect={setAccountFlow}
                                                onSaying={holdAccounts}
                                            />
                                        ) : null}
                                    </>
                                ) : null}
                            </div>
                        </Reveal>

                        <Reveal
                            show={soundsOpen}
                            label='Soundboard'
                        >
                            {soundsOpen ? (
                                <SoundboardSheet
                                    key={soundsKey}
                                    id={footerIds.sounds}
                                    version={snapshot.soundboard?.v || ''}
                                    rosterKey={rosterKey}
                                    onClose={closeSounds}
                                />
                            ) : null}
                        </Reveal>
                    </div>
                ) : null}

                {me && !linked ? (
                    <div className='vntv-dock'>
                        {/* Their stream, matched to them by username, is being
                            kept for clipping: said, with no switch and no
                            wipe, which linking it gives them (question 8). */}
                        {streamer?.kind === 'attributed' ? (
                            <p className='vntv-watchonly-note'>{streamer.notice}</p>
                        ) : null}

                        {/* A Twitch, Steam or Hytale account they connected
                            while they were linked: Disconnect, over the line
                            rather than in it, since a person can always
                            withdraw (design/lock-screen.md 9.2,
                            design/3.4.0.md 5.5). Connecting is for linked
                            people, so nothing else of the accounts is here. */}
                        <AccountsDisconnectKey
                            me={me}
                            where='watchonly'
                        />

                        <div
                            className='vntv-bar vntv-bar-watchonly'
                            role='group'
                            aria-label='Watch only'
                        >
                            <span className='vntv-watchonly'>
                                <EyeIcon size={12}/>
                                <span className='vntv-watchonly-word'>{'Watch only'}</span>
                            </span>
                            <button
                                ref={letInKeyRef}
                                type='button'
                                className='vntv-textkey'
                                aria-expanded={editingLink}
                                aria-controls={editingLink ? footerIds.link : undefined}
                                onClick={(event) => (editingLink ? closeLink() : openLink(event.currentTarget))}
                            >
                                <LinkIcon size={12}/>
                                {'Link your stream'}
                            </button>
                        </div>

                        <Reveal
                            show={editingLink}
                            label='Link your stream'
                        >
                            <div
                                id={footerIds.link}
                                className='vntv-sheet'
                                role='group'
                                aria-label='Link your stream'
                                onKeyDown={(event) => linkSheetKeys(event, linkBarRef.current, closeLink)}
                            >
                                <TokenBar
                                    variant='link'
                                    barRef={linkBarRef}
                                    focusOnMount={true}
                                    onLinked={linkedFromLetIn}
                                    onCancel={closeLink}
                                />
                            </div>
                        </Reveal>
                    </div>
                ) : null}

            </div>

            {banner.atBottom && <div className='vntv-banner'>{banner.text}</div>}

            {/* A hashtag's results, when one in a stream title is pressed:
                drawn over the whole window, wherever this sits. */}
            <HashtagSearchHost/>
        </div>
    );
};

// Where something sits, relative to whatever it is being measured against.
type Box = {x: number; y: number; w: number; h: number};

// Sends an element back to where it was and animates it home. Translation only:
// scaling as well would stretch the picture inside a tile for a third of a
// second, which looks worse than a tile that simply changes size.
function glide(element: HTMLElement, from: Box, to: Box): void {
    const dx = from.x - to.x;
    const dy = from.y - to.y;
    if (Math.abs(dx) < 1 && Math.abs(dy) < 1) {
        return;
    }

    element.animate([
        {transform: `translate(${dx}px, ${dy}px)`},
        {transform: 'translate(0, 0)'},
    ], {duration: GLIDE_MS, easing: 'cubic-bezier(0.2, 0.7, 0.2, 1)'});
}

// Whether focus can be put back on something: still in the page, not
// switched off, and not in a part of it that has gone inert.
function usable(element: HTMLElement | null): element is HTMLElement {
    return Boolean(element && element.isConnected && !element.matches(':disabled') && !element.closest('[inert]'));
}

// The keys of a sheet with a bearer token bar in it (Relink, Link your
// stream): Escape from anywhere in it closes it, as every footer sheet does —
// the bar has had the key first, and keeps it while there is an error to
// clear — and a paste or a letter pressed on its buttons goes into the bar.
function linkSheetKeys(event: React.KeyboardEvent<HTMLDivElement>, bar: HTMLInputElement | null, close: () => void): void {
    if (event.key === 'Escape' && !composing(event)) {
        event.preventDefault();
        event.stopPropagation();
        close();

        return;
    }

    keepTokenKeys(event, bar);
}
