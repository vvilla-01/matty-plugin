import React from 'react';
import {createPortal} from 'react-dom';

import {draftVideoURL} from '../client';
import {
    CLIP_CANNOT_PLAY,
    CLIP_NO_SOUND,
    PUBLISH_SLOW_MS,
    clampIn,
    clampOut,
    clampPosition,
    clipErrorCopy,
    containFit,
    cuttingNote,
    cuttingStatus,
    draftNotes,
    editorContext,
    playFrom,
    playability,
    playbackStep,
    publishedSentence,
    publishingStatus,
    readyStatus,
    selectionTime,
    tagTitleOf,
    thumbnailAt,
    trimFrom,
} from '../clipLogic';
import type {ClipErrorAction, ClipErrorContext, Trim} from '../clipLogic';
import {
    checkEditingDraft,
    clipErrorCodeOf,
    clipErrorTextOf,
    closeEditor,
    discardEditing,
    editsOf,
    isAccessRefusal,
    keepEdits,
    openClips,
    publishEditing,
    retryCapture,
} from '../clips';
import type {EditorState} from '../clips';
import {catchStrayKey, keepKeysInside, trapTab} from '../focusTrap';
import {openLock} from '../lockPending';
import {composing} from '../keys';
import {nameNow} from '../name';
import {getSessionInfo, takeSound} from '../sessions';
import {clockSeconds, clockTime, dayMonth, viewerZone} from '../time';
import {useTwoClicks} from '../twoClicks';
import {getAudio, setAudio, useAudio} from '../watching';

import {ClipTrimmer} from './ClipTrimmer';
import type {TrimmerControl} from './ClipTrimmer';
import {ShareClipButton, ShareClipStatus, useShareClip} from './ShareClipButton';
import {CheckIcon, CloseIcon, LinkIcon, LoopIcon, PauseIcon, PlayIcon, ScissorsIcon, SpeakerIcon, SpeakerMutedIcon} from './icons';
import {Plate, Screen} from './NoSignal';

// The clip editor (spec 3.6): what a press of the scissors opens, and Edit on
// a draft. The draft's picture with a trimmer under it, where it came from, a
// title, and Discard, Later and PUBLISH. Its state — cutting, ready,
// publishing, published, or what went wrong — is in clips.ts, and so is
// everything it asks of the server; this draws it and plays the draft.
//
// A portal on document.body, like the Clips dialog, which it takes the place
// of while it is open, and the same kind of card: Tab goes round inside it,
// Escape is caught on the document in the capture phase (so Mattermost does
// not also close whatever is under it, and so a drag of the trimmer can be
// cancelled — no React handler ever sees Escape past that listener), and a
// press that both starts and ends on the backdrop is Later. Later, the x,
// Escape and the backdrop all close it and keep the draft; while it publishes
// none of them do anything, and the publish is seen through.
//
// It opens at once, cutting, with the title ready to type into: the server
// takes a moment to cut the draft, and somebody who pressed the scissors on a
// moment knows what to call it. Only the picture, the trimmer and PUBLISH
// wait for the draft.
//
// For linked people only, like making clips. If the link goes while it is
// open — the identity says so, or a request is refused for access (locked,
// watch_only, or not_linked from an older server) — it locks: the picture is
// stopped and let go, the trimmer, the title and PUBLISH are inert, and it
// says why, with Link your stream and Later. No Discard there: somebody who is
// not linked cannot delete their own drafts either (Victor's answer to
// question B), so the server would refuse it. Going from linked to let in in
// this page closes it outright (access.ts); this is the belt for a draft open
// in another window.
//
// Keys (spec 3.5). Space, I and O are the editor's only on the picture or a
// trimmer thumb, and stopped there; on a button, a field or anything else
// they keep their own meaning. The card stops every other printable key in
// the bubble phase, so none of them reaches Mattermost's message box.
//
// The sound (spec 3.8 as the addendum's 2.2 corrects it). The draft plays
// with its sound, and only one thing in the page is ever audible: pressing
// play takes the sound from the stream that had it, and pausing, closing,
// Done or Escape gives it back — to that stream only if it is still playing,
// through takeSound, in the same press, which unmuting needs. Somebody taking
// the sound on a stream meanwhile pauses the draft. A browser that cannot
// play its sound (Safari, with Opus) plays it silent and takes nothing.

type Props = {
    state: NonNullable<EditorState>;

    // The reader's link, as the identity says it: the server decides again on
    // every request.
    linked: boolean;
};

// Ids for the card's title and the title field, unique per editor, from a
// counter (see TitleSearch); and not vntv-, which the style check reads as a
// class name.
let editors = 0;

// The picture published with the clip: one frame, 480 × 270 with black round
// it, as JPEG at 0.72, from a second <video> of the draft so the one on show
// is not disturbed. Skipped without a word — null — if it cannot be taken in
// four seconds, or at all.
const THUMB_WIDTH = 480;
const THUMB_HEIGHT = 270;
const THUMB_QUALITY = 0.72;
const THUMB_TIMEOUT_MS = 4000;

function captureThumbnail(src: string, atSeconds: number): Promise<Blob | null> {
    if (src === '') {
        return Promise.resolve(null);
    }

    return new Promise((resolve) => {
        const video = document.createElement('video');
        let done = false;
        const finish = (jpeg: Blob | null) => {
            if (done) {
                return;
            }

            done = true;
            window.clearTimeout(timer);
            video.removeAttribute('src');
            video.load();
            resolve(jpeg);
        };
        const timer = window.setTimeout(() => finish(null), THUMB_TIMEOUT_MS);

        video.muted = true;
        video.preload = 'auto';
        video.playsInline = true;
        video.addEventListener('loadedmetadata', () => {
            video.currentTime = atSeconds;
        }, {once: true});
        video.addEventListener('seeked', () => {
            try {
                const canvas = document.createElement('canvas');
                canvas.width = THUMB_WIDTH;
                canvas.height = THUMB_HEIGHT;
                const g = canvas.getContext('2d');
                if (!g) {
                    finish(null);

                    return;
                }

                g.fillStyle = '#000000';
                g.fillRect(0, 0, THUMB_WIDTH, THUMB_HEIGHT);
                const fit = containFit(video.videoWidth, video.videoHeight, THUMB_WIDTH, THUMB_HEIGHT);
                g.drawImage(video, fit.x, fit.y, fit.width, fit.height);
                canvas.toBlob((jpeg) => finish(jpeg), 'image/jpeg', THUMB_QUALITY);
            } catch {
                // A canvas the browser will not read back, or a frame it will
                // not draw.
                finish(null);
            }
        }, {once: true});
        video.addEventListener('error', () => finish(null), {once: true});
        video.src = src;
    });
}

// What the browser said when the draft's file would not load. The decoder
// failing is the browser's own doing; anything else is asked about.
const MEDIA_DECODE_FAILED = 3;

const SOUND_TIP = 'The clip has the sound while it plays. Pause it and the stream gets it back.';
const CLOSE_TIP = 'Close — your draft is kept for 2 hours';
const DRAFT_LOAD_FAILED = 'The draft could not be loaded just now.';

const ACTION_WORDS: Partial<Record<ClipErrorAction, string>> = {
    retry: 'Try again',
    drafts: 'Open drafts',
    clips: 'Open Clips',
    yours: 'Your clips',
};

// Where focus goes back to when the editor closes: the scissors that opened
// it, while they are still in the page. Their tile can change while the
// editor is open — swapped when the stream drops or comes back, or given up
// once it has waited long enough — and then the tile in its place takes
// focus, or the panel itself: not the page's body, from where the next
// letter typed would go to Mattermost's message box. Anything else that has
// gone (the dialog's Clip the end, closed under the editor) has nothing in
// its place, and null.
function returnTargetOf(back: HTMLElement | null | undefined, handle: string): HTMLElement | null {
    if (!back) {
        return null;
    }
    if (back.isConnected) {
        return back;
    }
    if (!back.closest('.vntv-player')) {
        return null;
    }

    return document.querySelector<HTMLElement>(`.vntv-panel .vntv-tile[data-handle="${CSS.escape(handle)}"] > .vntv-player`) ||
        document.querySelector<HTMLElement>('.vntv-panel');
}

// React 18 has no inert prop (19 adds it); the attribute is set on the
// element itself.
function useInert(ref: React.RefObject<HTMLElement>, inert: boolean): void {
    React.useLayoutEffect(() => {
        const element = ref.current;
        if (!element) {
            return;
        }

        if (inert) {
            element.setAttribute('inert', '');
        } else {
            element.removeAttribute('inert');
        }
    });
}

export const ClipEditor: React.FC<Props> = ({state, linked}) => {
    const draft = state.draft;
    const phase = state.phase;
    const locked = !linked || isAccessRefusal(state.error || '');
    const publishing = phase === 'publishing';
    const checking = Boolean(state.checking);
    // Publishing, or looking for a publish whose answer was lost: nothing
    // else is sent meanwhile. Only the publish itself is seen through; while
    // it is only being looked for, the editor can still be closed, and the
    // clip, if it was published, is in Clips.
    const busy = publishing || checking;
    const gone = state.failed === 'draft';
    const zone = viewerZone();

    const [ids] = React.useState(() => {
        editors += 1;

        return {heading: `villanet-clipedit-title-${editors}`, title: `villanet-clipedit-name-${editors}`};
    });

    const card = React.useRef<HTMLDivElement>(null);
    const stage = React.useRef<HTMLDivElement>(null);
    const trimBox = React.useRef<HTMLDivElement>(null);
    const titleBox = React.useRef<HTMLDivElement>(null);
    const titleInput = React.useRef<HTMLInputElement>(null);
    const publishButton = React.useRef<HTMLButtonElement>(null);
    const doneButton = React.useRef<HTMLButtonElement>(null);
    const alertBox = React.useRef<HTMLDivElement>(null);
    const video = React.useRef<HTMLVideoElement | null>(null);
    const trimmer = React.useRef<TrimmerControl | null>(null);

    // ---- What has been typed and trimmed ----

    // The title is live from the moment the editor opens; what was typed for
    // this draft before (Later, then Edit) comes back.
    const [title, setTitle] = React.useState(() => (draft ? editsOf(draft.id)?.title || '' : ''));

    // The selection: the whole draft to begin with, and only ever shrunk.
    const [trim, setTrimState] = React.useState<Trim>(() => {
        if (!draft) {
            return trimFrom(state.lengthSeconds * 1000);
        }

        const kept = editsOf(draft.id);

        return kept ? {inMs: kept.inMs, outMs: kept.outMs, durationMs: draft.durationMs} : trimFrom(draft.durationMs);
    });

    // The draft arriving after the cut: the selection is all of it.
    const draftId = draft?.id || '';
    React.useEffect(() => {
        if (draft && trim.durationMs !== draft.durationMs) {
            const kept = editsOf(draft.id);
            setTrimState(kept ? {inMs: kept.inMs, outMs: kept.outMs, durationMs: draft.durationMs} : trimFrom(draft.durationMs));
        }
    }, [draftId]);

    // Kept as they change, in memory, for this draft only.
    React.useEffect(() => {
        if (draft && trim.durationMs === draft.durationMs) {
            keepEdits(draft.id, {title, inMs: trim.inMs, outMs: trim.outMs});
        }
    }, [draftId, title, trim]);

    // ---- Playing it ----

    const [positionMs, setPositionMs] = React.useState(trim.inMs);
    const [playing, setPlaying] = React.useState(false);
    const [loop, setLoop] = React.useState(true);
    const [muted, setMuted] = React.useState(true);
    const userMuted = React.useRef(false);
    const [play, setPlay] = React.useState<'ok' | 'no-video' | 'no-sound'>('ok');
    const [failure, setFailure] = React.useState<'' | 'asking' | 'cannot' | 'unloaded'>('');

    // For the frame loop and the handlers, which run between renders.
    const now = React.useRef({trim, loop, positionMs});
    now.current = {trim, loop, positionMs};

    // The stream that had the sound before the draft took it, or undefined
    // while the draft does not have it; null is "nothing had it".
    const held = React.useRef<string | null | undefined>(undefined);
    const take = () => {
        if (held.current === undefined) {
            held.current = getAudio();
            setAudio(null);
        }
    };
    const giveBack = () => {
        const before = held.current;
        held.current = undefined;
        if (before && getSessionInfo(before)?.status === 'playing') {
            takeSound(before);
        }
    };

    // Somebody took the sound for a stream while the draft had it.
    const audio = useAudio();
    React.useEffect(() => {
        if (audio !== null && held.current !== undefined) {
            held.current = undefined;
            video.current?.pause();
        }
    }, [audio]);

    // The <video>, which comes and goes with the draft and the link. One that
    // goes is emptied first: taken out of the page, it does not stop
    // downloading in every browser.
    const setVideo = React.useCallback((element: HTMLVideoElement | null) => {
        const before = video.current;
        if (before && before !== element) {
            before.pause();
            before.removeAttribute('src');
            before.load();
        }

        video.current = element;
        if (element) {
            // It starts muted: the sound is taken only by a press of play.
            element.muted = true;
        }
    }, []);

    const pause = () => {
        video.current?.pause();
        giveBack();
    };

    const startPlaying = () => {
        const element = video.current;
        if (!element || locked || !draft || gone) {
            return;
        }

        const from = playFrom(now.current.positionMs, now.current.trim);
        if (Math.abs((element.currentTime * 1000) - from) > 1) {
            element.currentTime = from / 1000;
            setPositionMs(from);
        }
        if (play !== 'no-sound' && !userMuted.current) {
            take();
            element.muted = false;
            setMuted(false);
        }
        element.play().catch(() => {
            // Refused, or interrupted by a pause; the button says it is
            // paused either way.
        });
    };

    const togglePlay = () => {
        if (video.current && !video.current.paused) {
            pause();
        } else {
            startPlaying();
        }
    };

    const toggleMute = () => {
        const element = video.current;
        if (!element || play === 'no-sound') {
            return;
        }

        const next = !element.muted;
        userMuted.current = next;
        element.muted = next;
        setMuted(next);
        if (next) {
            giveBack();
        } else if (!element.paused) {
            take();
        }
    };

    const seek = (ms: number) => {
        const element = video.current;
        if (element) {
            element.currentTime = ms / 1000;
        }
        setPositionMs(ms);
    };

    const setTrim = (inMs: number, outMs: number) => {
        setTrimState((was) => (was.inMs === inMs && was.outMs === outMs ? was : {...was, inMs, outMs}));
    };

    // I and O: the start or the end at the playhead.
    const mark = (edge: 'in' | 'out') => {
        const at = now.current.trim;
        if (edge === 'in') {
            setTrim(clampIn(now.current.positionMs, at), at.outMs);
        } else {
            setTrim(at.inMs, clampOut(now.current.positionMs, at));
        }
    };

    // Kept inside the selection while it plays: round again at the end with
    // Loop on, stopped there with it off, and brought back to the start if
    // the start was moved past it.
    React.useEffect(() => {
        if (!playing) {
            return undefined;
        }

        let frame = 0;
        const step = () => {
            const element = video.current;
            if (!element) {
                return;
            }

            const at = element.currentTime * 1000;
            const next = playbackStep(at, now.current.trim, now.current.loop);
            if (next.action === 'jump') {
                element.currentTime = next.toMs / 1000;
                setPositionMs(next.toMs);
            } else if (next.action === 'stop') {
                element.pause();
                element.currentTime = next.atMs / 1000;
                setPositionMs(next.atMs);
                giveBack();

                return;
            } else {
                setPositionMs(at);
            }
            frame = window.requestAnimationFrame(step);
        };

        frame = window.requestAnimationFrame(step);

        return () => window.cancelAnimationFrame(frame);
    }, [playing]);

    // Whether this browser can play it at all, and with its sound.
    React.useEffect(() => {
        const element = video.current;
        if (draft && element) {
            setPlay(playability(draft.codecs || '', (type) => element.canPlayType(type)));
        }
    }, [draftId]);

    // The file would not load: asked about, unless it was let go on purpose.
    const alive = React.useRef(true);
    React.useEffect(() => () => {
        alive.current = false;
    }, []);

    const failed = (event: React.SyntheticEvent<HTMLVideoElement>) => {
        const element = event.currentTarget;
        if (!element.hasAttribute('src') || play === 'no-video') {
            return;
        }

        // Publishing: the server moves the draft's file as it makes the
        // clip, so a request for it then can fail. That is not news; the
        // publish's own answer is.
        if (publishing) {
            return;
        }
        if (element.error?.code === MEDIA_DECODE_FAILED) {
            setFailure('cannot');

            return;
        }

        setFailure('asking');
        checkEditingDraft().then((answer) => {
            if (!alive.current) {
                return;
            }

            // There, so the browser could not play it; gone, which the
            // editor now says itself; or nobody could say.
            if (answer === 'ok') {
                setFailure('cannot');
            } else {
                setFailure(answer === 'gone' ? '' : 'unloaded');
            }
        });
    };

    const reload = (event: React.MouseEvent<HTMLButtonElement>) => {
        if (document.activeElement === event.currentTarget) {
            stage.current?.focus({preventScroll: true});
        }
        setFailure('');
        video.current?.load();
    };

    // Locked: the picture goes (setVideo empties it as it does), so nothing
    // more of it is fetched for somebody who may no longer see it, and the
    // sound goes back. Linked again, a new one loads it afresh, muted.
    React.useLayoutEffect(() => {
        if (locked) {
            giveBack();
            setPlaying(false);
            setMuted(true);
        }
    }, [locked]);

    // Going: the sound goes back in the same press that closed it. The
    // picture is emptied by setVideo as it goes. A layout effect, for the
    // press.
    React.useLayoutEffect(() => () => {
        giveBack();
    }, []);

    // ---- Publishing ----

    // Still working after ten seconds says so.
    const [slow, setSlow] = React.useState(false);
    React.useEffect(() => {
        setSlow(false);
        if (!publishing) {
            return undefined;
        }

        const timer = window.setTimeout(() => setSlow(true), PUBLISH_SLOW_MS);

        return () => window.clearTimeout(timer);
    }, [publishing]);

    const canPublish = !locked && Boolean(draft) && !gone && !checking &&
        (phase === 'ready' || (phase === 'error' && state.failed === 'publish'));

    const publish = () => {
        if (!canPublish || !draft) {
            return;
        }

        pause();

        // The fields go inert while it publishes, and focus in them would
        // fall out of the dialog: it waits on PUBLISH, which says
        // "Publishing…".
        const active = document.activeElement;
        if (active && [stage, trimBox, titleBox, alertBox].some((box) => box.current?.contains(active))) {
            publishButton.current?.focus({preventScroll: true});
        }

        const at = now.current.trim;
        const src = draftVideoURL(draft.id);
        publishEditing({title: title.trim(), inMs: at.inMs, outMs: at.outMs}, () => captureThumbnail(src, thumbnailAt(at)));
    };

    // ---- Closing ----

    // Where focus goes back to: the scissors that opened it, or the tile now
    // in their place (returnTargetOf), and otherwise nowhere in particular —
    // the page's body — rather than anything the reader did not choose. An
    // editor opened from the dialog's drafts goes back to the dialog, which
    // takes focus itself.
    const finish = (backToList = true) => {
        const back = returnTargetOf(state.returnFocus, state.handle);
        pause();
        closeEditor(backToList);
        if (back) {
            back.focus({preventScroll: true});
        } else if (card.current?.contains(document.activeElement)) {
            (document.activeElement as HTMLElement | null)?.blur?.();
        }
    };

    const later = () => {
        if (!publishing) {
            finish();
        }
    };

    const twice = useTwoClicks();

    // Share, once published: the clip the draft became. Kept with the rest
    // of the editor's state, so hooks run the same in every phase.
    const share = useShareClip(state.clip?.id || '');
    const [discarding, setDiscarding] = React.useState(false);
    const [discardError, setDiscardError] = React.useState('');
    const discard = (event: React.MouseEvent) => {
        if (busy || discarding || !twice.press('discard', event)) {
            return;
        }

        // Discarded, the editor closes with the press's button in it, and
        // focus goes back where finish() sends it: to the scissors, while
        // they are there, rather than to the page's body, where a keyboard
        // user has lost their place and C clips nothing. Only when focus went
        // with the editor: one opened from the dialog's drafts goes back to
        // the dialog, which takes focus itself, and a press of Later
        // meanwhile has already sent it back.
        const back = state.returnFocus;
        const handle = state.handle;
        pause();
        setDiscarding(true);
        setDiscardError('');
        discardEditing().then(() => {
            const active = document.activeElement;
            const target = returnTargetOf(back, handle);
            if (target && (!active || active === document.body)) {
                target.focus({preventScroll: true});
            }
        }, (reason: unknown) => {
            if (alive.current) {
                setDiscardError(clipErrorCopy(clipErrorCodeOf(reason), 'draft', {brand: nameNow()}, clipErrorTextOf(reason)).text);
            }
        }).then(() => {
            if (alive.current) {
                setDiscarding(false);
            }
        });
    };

    // Escape and Tab on the document, in the capture phase, as the Clips
    // dialog does: a drag of the trimmer is cancelled first, a publish is
    // never walked away from, and otherwise Escape is Later. A letter typed
    // while focus is on nothing at all is kept from the message box here.
    const escape = React.useRef<() => void>(later);
    escape.current = () => {
        if (trimmer.current?.cancelDrag()) {
            return;
        }

        later();
    };

    React.useEffect(() => {
        const onKey = (event: KeyboardEvent) => {
            if (event.key === 'Escape') {
                event.stopPropagation();
                escape.current();

                return;
            }

            if (catchStrayKey(event, card.current)) {
                return;
            }

            trapTab(event, card.current);
        };

        document.addEventListener('keydown', onKey, true);

        return () => document.removeEventListener('keydown', onKey, true);
    }, []);

    // Focus. Opening, the title, which is live from the start. Published, on
    // Done; locked, on Link your stream. Before paint.
    React.useLayoutEffect(() => {
        titleInput.current?.focus({preventScroll: true});
    }, []);

    React.useLayoutEffect(() => {
        if (phase === 'published') {
            doneButton.current?.focus({preventScroll: true});
        }
    }, [phase]);

    React.useLayoutEffect(() => {
        if (locked) {
            alertBox.current?.querySelector<HTMLElement>('.vntv-link-cta')?.focus({preventScroll: true});
        }
    }, [locked]);

    // The C that opened the editor may still be held down. Its repeats land
    // in the title, which has focus by then, and would type "ccc": they are
    // dropped until that key comes up, or another is pressed.
    const openingKey = React.useRef(true);

    // A press that both starts and ends on the backdrop.
    const downOnBackdrop = React.useRef(false);

    // ---- What is inert ----

    const noPicture = !draft || gone || locked;

    // Whether closing keeps a draft: one there, or one being cut, which the
    // server keeps whether or not anybody waits for it.
    const keeps = (Boolean(draft) && !gone) || phase === 'cutting';
    useInert(stage, phase === 'cutting' || publishing || noPicture);
    useInert(trimBox, phase === 'cutting' || publishing || noPicture);
    useInert(titleBox, publishing || locked);
    useInert(publishButton, phase === 'cutting' || locked);

    // Linked again while it was locked: Link your stream, which had focus,
    // has gone with the alert, so focus goes to the title, live again — or,
    // once published, to Done. After the inert effects above, which have
    // just let the title go. Only when focus fell out of the card.
    const wasLocked = React.useRef(locked);
    React.useLayoutEffect(() => {
        const before = wasLocked.current;
        wasLocked.current = locked;
        const active = document.activeElement;
        if (before && !locked && (!active || active === document.body || !card.current?.contains(active))) {
            (titleInput.current || doneButton.current || card.current)?.focus({preventScroll: true});
        }
    }, [locked]);

    // ---- Words ----

    // What it is doing, for the line read out as it changes. Nothing while
    // locked: the alert says it all.
    const length = Math.max(0, trim.outMs - trim.inMs);
    let status = '';
    if (locked) {
        status = '';
    } else if (phase === 'cutting') {
        status = cuttingStatus(state.lengthSeconds, state.streamName);
    } else if (phase === 'ready' && draft) {
        status = readyStatus(draft.durationMs);
    } else if (publishing) {
        status = publishingStatus(length, slow);
    } else if (checking) {
        status = 'Checking whether it was published…';
    }

    let alert: {text: string; actions: ClipErrorAction[]} | null = null;
    if (locked) {
        alert = clipErrorCopy('watch_only', 'editor', {keptUntil: draft ? clockTime(draft.expiresAt, zone) : '', brand: nameNow()});
    } else if (phase === 'error' && state.error && !checking) {
        let context: ClipErrorContext = 'publish';
        if (state.failed === 'capture') {
            context = state.source === 'tail' ? 'tail' : 'capture';
        }
        alert = clipErrorCopy(state.error, context, {lengthSeconds: state.lengthSeconds, name: state.streamName, brand: nameNow()}, state.errorText || '');
    }

    const act = (action: ClipErrorAction) => {
        switch (action) {
        case 'retry':
            if (state.failed === 'capture') {
                retryCapture();
                titleInput.current?.focus({preventScroll: true});
            } else {
                publish();
            }
            break;
        case 'drafts':
        case 'clips':
            finish(false);
            openClips();
            break;
        case 'yours':
            finish(false);
            openClips(undefined, 'yours');
            break;
        case 'link':
            // The bearer token bar: in the panel's footer for somebody let in,
            // or the lock for anybody else (lockPending.ts).
            finish(false);
            openLock();
            break;
        case 'later':
            later();
            break;
        default:
            break;
        }
    };

    let playMessage = '';
    if (play === 'no-video' || failure === 'cannot') {
        playMessage = CLIP_CANNOT_PLAY;
    } else if (play === 'no-sound') {
        playMessage = CLIP_NO_SOUND;
    }

    // ---- The picture ----

    let plate: {title: string; note?: string} | null = null;
    if (phase === 'cutting') {
        plate = {title: 'Cutting', note: cuttingNote(state.lengthSeconds, state.streamName)};
    } else if (locked) {
        plate = {title: 'Not linked', note: 'link your stream to see it'};
    } else if (gone) {
        plate = {title: 'Gone', note: 'this draft is not kept any more'};
    } else if (!draft) {
        plate = {title: 'Not cut', note: `nothing was cut from ${state.streamName}`};
    }

    const stageKey = (event: React.KeyboardEvent<HTMLDivElement>) => {
        if (event.target !== event.currentTarget || event.ctrlKey || event.metaKey || event.altKey || composing(event)) {
            return;
        }

        const key = event.key;
        if (key === ' ' || key === 'Spacebar') {
            event.preventDefault();
            event.stopPropagation();
            togglePlay();
        } else if (key === 'i' || key === 'I' || key === 'o' || key === 'O') {
            event.preventDefault();
            event.stopPropagation();
            mark(key.toLowerCase() === 'i' ? 'in' : 'out');
        }
    };

    const showBigPlay = !plate && !playing && !publishing && play !== 'no-video' && failure === '';
    const stageView = (
        <div
            ref={stage}
            className='vntv-clipedit-stage'
            tabIndex={0}
            role='group'
            aria-label='The draft. Space plays or pauses it; I and O set the start and the end of the clip at the playhead.'
            data-playing={playing ? 'true' : undefined}
            onKeyDown={stageKey}
        >
            {plate ? (
                <Screen noise={phase === 'cutting'}>
                    <Plate
                        title={plate.title}
                        note={plate.note}
                    />
                </Screen>
            ) : (
                <video
                    ref={setVideo}
                    className='vntv-clipedit-video'
                    src={draft && !locked ? draftVideoURL(draft.id) : undefined}
                    preload='auto'
                    playsInline={true}
                    disablePictureInPicture={true}
                    controlsList='nodownload'
                    aria-hidden={true}
                    onClick={togglePlay}
                    onLoadedMetadata={(event) => {
                        // Paused on the frame the clip starts on.
                        const from = now.current.trim.inMs;
                        event.currentTarget.currentTime = from / 1000;
                        setPositionMs(from);
                    }}
                    onLoadedData={() => {
                        if (failure !== '') {
                            setFailure('');
                        }
                    }}
                    onTimeUpdate={(event) => {
                        if (event.currentTarget.paused) {
                            setPositionMs(event.currentTarget.currentTime * 1000);
                        }
                    }}
                    onPlay={() => setPlaying(true)}
                    onPause={() => {
                        setPlaying(false);
                        giveBack();
                    }}
                    onEnded={(event) => {
                        // The end of the draft is the end of the selection.
                        const element = event.currentTarget;
                        if (now.current.loop) {
                            element.currentTime = now.current.trim.inMs / 1000;
                            element.play().catch(() => undefined);
                        } else {
                            setPlaying(false);
                            giveBack();
                        }
                    }}
                    onError={failed}
                />
            )}

            {showBigPlay ? (
                <button
                    type='button'
                    className='vntv-clipedit-bigplay'
                    aria-label='Play the clip'
                    title={play === 'no-sound' ? 'Play the clip' : SOUND_TIP}
                    onClick={startPlaying}
                ><PlayIcon size={30}/></button>
            ) : null}

            {plate ? null : (
                <div className='vntv-clipedit-bar'>
                    <button
                        type='button'
                        className='vntv-clipedit-control'
                        aria-label={playing ? 'Pause' : 'Play'}
                        title={playing || play === 'no-sound' ? undefined : SOUND_TIP}
                        onClick={togglePlay}
                    >{playing ? <PauseIcon size={16}/> : <PlayIcon size={16}/>}</button>
                    <span className='vntv-clipedit-time'>{selectionTime(trim, positionMs)}</span>
                    <span className='vntv-clipedit-spacer'/>
                    <button
                        type='button'
                        className='vntv-clipedit-control'
                        aria-pressed={loop}
                        aria-label='Loop'
                        title={loop ? 'Loop: on — it goes round again at the end' : 'Loop: off — it stops at the end'}
                        onClick={() => setLoop((was) => !was)}
                    ><LoopIcon size={16}/></button>
                    {/* No aria-pressed: the name flips, as the stream's own
                        sound button's does (Player.tsx). */}
                    <button
                        type='button'
                        className='vntv-clipedit-control'
                        aria-label={muted ? 'Unmute' : 'Mute'}
                        aria-disabled={play === 'no-sound' ? true : undefined}
                        title={play === 'no-sound' ? CLIP_NO_SOUND : undefined}
                        onClick={toggleMute}
                    >{muted ? <SpeakerMutedIcon size={16}/> : <SpeakerIcon size={16}/>}</button>
                </div>
            )}

            {publishing ? (
                <span
                    className='vntv-clipedit-progress'
                    aria-hidden={true}
                ><span className='vntv-clipedit-progress-bar'/></span>
            ) : null}
        </div>
    );

    // ---- The body ----

    let body: React.ReactNode;
    if (phase === 'published' && state.clip) {
        const clip = state.clip;
        body = (
            <div className='vntv-clipedit-done'>
                <p className='vntv-clipedit-done-mark'>
                    <CheckIcon size={18}/>
                    {'Published'}
                </p>
                <p className='vntv-clipedit-done-text'>{publishedSentence(clip, dayMonth(clip.expiresAt, zone))}</p>
                <div className='vntv-clipedit-actions vntv-clipedit-done-actions'>
                    <button
                        type='button'
                        className='vntv-clips-btn'
                        onClick={() => {
                            finish(false);
                            openClips(clip.id);
                        }}
                    >{'Watch it'}</button>

                    {/* Between Watch it and Done (addendum 4.10). Not for
                        somebody whose link went meanwhile: the server would
                        refuse them, and Watch it shows them why. */}
                    {locked ? null : (
                        <ShareClipButton
                            share={share}
                            className='vntv-clips-btn'
                        />
                    )}

                    <button
                        ref={doneButton}
                        type='button'
                        className='vntv-clips-btn vntv-clipedit-primary'
                        onClick={() => finish()}
                    >{'Done'}</button>
                </div>
                {locked ? null : <ShareClipStatus share={share}/>}
            </div>
        );
    } else {
        const notes = draft && !gone && !locked ? draftNotes(draft) : [];
        const context = draft ? editorContext({
            streamer: draft.streamer,
            tag: tagTitleOf(draft.tag),
            note: draft.streamNote || '',
            from: clockSeconds(draft.startAt + trim.inMs, zone),
            to: clockSeconds(draft.startAt + trim.outMs, zone),
            tail: state.tail || (state.source === 'tail' ? 'unknown' : undefined),
        }) : '';

        let publishWords = 'Publish';
        if (publishing) {
            publishWords = 'Publishing…';
        }

        body = (
            <>
                {stageView}

                <p className='vntv-clipedit-context'>{context}</p>

                {notes.map((note) => (
                    <p
                        key={note}
                        className='vntv-clipedit-note'
                    >{note}</p>
                ))}
                {playMessage !== '' && !plate ? (
                    <p
                        className='vntv-clipedit-note'
                        role='status'
                    >{playMessage}</p>
                ) : null}
                {failure === 'unloaded' && !plate ? (
                    <p
                        className='vntv-clipedit-note vntv-clipedit-failed'
                        role='alert'
                    >
                        <span>{DRAFT_LOAD_FAILED}</span>
                        <button
                            type='button'
                            className='vntv-clips-btn'
                            onClick={reload}
                        >{'Try again'}</button>
                    </p>
                ) : null}

                {/* While it cuts, the selection the draft will have, inert;
                    nothing at all once nothing was cut, which a full
                    "2:00 of 2:00" would say had been. */}
                {draft || phase === 'cutting' ? (
                    <div
                        ref={trimBox}
                        className='vntv-clipedit-trim'
                    >
                        <ClipTrimmer
                            durationMs={trim.durationMs}
                            inMs={trim.inMs}
                            outMs={trim.outMs}
                            positionMs={clampPosition(positionMs, trim.durationMs)}
                            onChange={setTrim}
                            onSeek={seek}
                            onScrubStart={pause}
                            onScrubEnd={() => undefined}
                            onTogglePlay={togglePlay}
                            onMark={mark}
                            control={trimmer}
                        />
                    </div>
                ) : null}

                <div
                    ref={titleBox}
                    className='vntv-clipedit-title'
                >
                    <label
                        className='vntv-clipedit-label'
                        htmlFor={ids.title}
                    >{'Title'}</label>
                    <input
                        ref={titleInput}
                        id={ids.title}
                        className='vntv-clipedit-input'
                        type='text'
                        value={title}
                        maxLength={80}
                        placeholder='What happened?'
                        autoComplete='off'
                        spellCheck={true}
                        onChange={(event) => setTitle(event.target.value)}
                        onKeyUp={() => {
                            openingKey.current = false;
                        }}
                        onKeyDown={(event) => {
                            if (openingKey.current) {
                                if (event.repeat && (event.key === 'c' || event.key === 'C')) {
                                    event.preventDefault();

                                    return;
                                }
                                openingKey.current = false;
                            }
                            if (event.key === 'Enter' && !composing(event)) {
                                event.preventDefault();
                                publish();
                            }
                        }}
                    />
                    {title.length >= 60 ? (
                        <span className='vntv-clipedit-count'>{`${title.length} / 80`}</span>
                    ) : null}
                </div>

                <div
                    ref={alertBox}
                    className='vntv-clipedit-alert'
                    role='alert'
                >
                    {alert ? (
                        <>
                            <p className='vntv-clipedit-alert-text'>{alert.text}</p>
                            {alert.actions.some((action) => action !== 'check') ? (
                                <span className='vntv-clipedit-alert-actions'>
                                    {alert.actions.map((action) => {
                                        if (action === 'link') {
                                            return (
                                                <button
                                                    key={action}
                                                    type='button'
                                                    className='vntv-link-cta'
                                                    onClick={() => act(action)}
                                                >
                                                    <LinkIcon size={15}/>
                                                    {'Link your stream'}
                                                </button>
                                            );
                                        }
                                        if (action === 'later' || action === 'discard' || action === 'check') {
                                            // Later and Discard are on the
                                            // row below already.
                                            return null;
                                        }

                                        return (
                                            <button
                                                key={action}
                                                type='button'
                                                className='vntv-clips-btn'
                                                onClick={() => act(action)}
                                            >{ACTION_WORDS[action] || action}</button>
                                        );
                                    })}
                                </span>
                            ) : null}
                        </>
                    ) : null}
                    {discardError !== '' ? <p className='vntv-clipedit-alert-text'>{discardError}</p> : null}
                </div>

                <div className='vntv-clipedit-actions'>
                    {draft && !gone && !locked ? (
                        <button
                            type='button'
                            className='vntv-clips-btn'
                            data-action='discard'
                            aria-disabled={busy || discarding ? true : undefined}
                            data-armed={twice.armed === 'discard' ? 'true' : undefined}
                            onClick={discard}
                        >{(() => {
                                if (discarding) {
                                    return 'Discarding…';
                                }

                                return twice.armed === 'discard' ? 'Click again to discard' : 'Discard';
                            })()}</button>
                    ) : null}
                    <span className='vntv-clipedit-spacer'/>
                    <button
                        type='button'
                        className='vntv-clips-btn'
                        aria-disabled={publishing ? true : undefined}
                        title={keeps ? CLOSE_TIP : undefined}
                        onClick={later}
                    >{keeps ? 'Later' : 'Close'}</button>
                    <button
                        ref={publishButton}
                        type='button'
                        className='vntv-clipedit-publish'
                        aria-disabled={canPublish ? undefined : true}
                        onClick={publish}
                    >{publishWords}</button>
                </div>
            </>
        );
    }

    return createPortal(
        <div
            className='vntv vntv-modal vntv-clipedit-modal'
            role='presentation'
            onMouseDown={(event) => {
                downOnBackdrop.current = event.target === event.currentTarget;
            }}
            onMouseUp={(event) => {
                const both = downOnBackdrop.current && event.target === event.currentTarget;
                downOnBackdrop.current = false;
                if (both) {
                    later();
                }
            }}
        >
            <div
                ref={card}
                className='vntv-modal-card vntv-clipedit'
                data-phase={phase}
                data-locked={locked ? 'true' : undefined}
                role='dialog'
                aria-modal={true}
                aria-labelledby={ids.heading}
                tabIndex={-1}
                onMouseDown={(event) => event.stopPropagation()}
                onKeyDown={keepKeysInside}
            >
                <header className='vntv-modal-head'>
                    <span
                        className='vntv-modal-icon'
                        aria-hidden={true}
                    ><ScissorsIcon size={20}/></span>
                    <h2
                        id={ids.heading}
                        className='vntv-modal-title'
                    >{'New clip'}</h2>
                    <button
                        type='button'
                        className='vntv-modal-close'
                        aria-label={keeps && phase !== 'published' ? CLOSE_TIP : 'Close'}
                        title={keeps && phase !== 'published' ? CLOSE_TIP : 'Close'}
                        aria-disabled={publishing ? true : undefined}
                        onClick={() => {
                            if (phase === 'published') {
                                finish();
                            } else {
                                later();
                            }
                        }}
                    ><CloseIcon size={16}/></button>
                </header>

                <div className='vntv-modal-body vntv-clipedit-body'>
                    {body}

                    {/* Always there, so what is put in it is announced: what
                        it is doing, and once published what that means. */}
                    <p
                        className={phase === 'published' ? 'vntv-sr-only' : 'vntv-clipedit-status'}
                        role='status'
                    >{phase === 'published' && state.clip ? publishedSentence(state.clip, dayMonth(state.clip.expiresAt, zone)) : status}</p>

                    {/* What Discard's first press changed it to, read out:
                        a change of the focused button's name is not spoken
                        by every screen reader, and the press would otherwise
                        seem to do nothing. Heard, not seen. */}
                    <p
                        className='vntv-sr-only'
                        role='status'
                    >{twice.armed === 'discard' && !busy && !discarding ? 'Click again to discard.' : ''}</p>
                </div>
            </div>
        </div>,
        document.body,
    );
};
