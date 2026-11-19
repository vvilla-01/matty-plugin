import React from 'react';

import {
    CARD_GONE,
    CLIP_CANNOT_PLAY,
    CLIP_NOT_LOADED,
    DOWNLOAD_STARTED,
    actionAfterLoss,
    agoWords,
    clipDownloadOffered,
    clipErrorCopy,
    clipTime,
    deleteConfirm,
    goesWords,
    streamLabel,
    tagLine,
    tagTitleOf,
} from '../clipLogic';
import {clipErrorCodeOf, clipErrorTextOf, deleteOn, downloadClip, getClip, isAccessRefusal, loadClip, reportOn, shownTitleOf, useClip} from '../clips';
import {useAccess} from '../identity';
import {useFocusHome} from '../focusHome';
import {focusStops} from '../focusTrap';
import {nameNow} from '../name';
import {useTwoClicks} from '../twoClicks';
import type {ClipTag, Me} from '../types';

import {CategoryIcon} from './categories';
import {ClipName, UpvotePill} from './ClipCard';
import {ClipTile} from './ClipTile';
import type {PlayerSays} from './ClipTile';
import {MediaProfile} from './MediaProfile';
import {Screen} from './NoSignal';
import {ShareClipButton, ShareClipStatus, useShareClip} from './ShareClipButton';

// One clip, played in the Clips dialog: the video, its title and upvotes, who
// made it from whose stream, and what the reader may do about it. A view of
// the dialog, not a dialog of its own; Back returns to the list where it was.
//
// The video plays in the stream tile's own frame (ClipTile.tsx, from 3.3.0),
// and Download is one of its controls: from 3.2.1 the one way to download a
// clip, while an admin allows it and its streamer has not said no
// (design/3.2.1.md 1.6, design/3.3.0.md). What Download says goes on the
// tile's line under the picture; Report and Delete say theirs under the
// buttons.

// What a file that would not load is called when nothing better is known.
const FAILED_TO_LOAD = CLIP_NOT_LOADED;

// What the clip's stream was tagged with when it was cut (addendum 4.14),
// on a line under the title: "Hytale · Game · 2026". When lookups found the
// real game, movie or show it is a button, which opens the same little
// profile what is on a live stream opens (NowPlaying.tsx): the cover, what it
// is, and where to read more. Words otherwise, and nothing for no tag.
//
// The profile hangs beside the button, above everything in the dialog (layer
// 'modal'), and is drawn inside the dialog's card: so the dialog's Tab trap
// counts it as the dialog's own, and a screen reader that hides everything
// outside an aria-modal dialog still reads it. Escape closes it before the
// dialog hears it: the dialog listens on the document in the capture phase,
// so this listens on the window, which an event passes through first. So
// does Tab, for the one thing the trap cannot know: the profile is drawn at
// the end of the card, but belongs after the button that opened it. Tab
// past its last stop closes it and goes on to whatever follows the button;
// Shift+Tab before its first closes it and goes back to the button.

// Ids for the profile, unique per line, from a counter; see TitleSearch.
let nextTagProfile = 0;

const ClipTagLine: React.FC<{tag?: ClipTag; who: string}> = ({tag, who}) => {
    const chip = React.useRef<HTMLButtonElement | null>(null);
    const [open, setOpen] = React.useState<{keyboard: boolean} | null>(null);
    const [profileId] = React.useState(() => {
        nextTagProfile += 1;

        return `villanet-cliptag-${nextTagProfile}`;
    });

    React.useEffect(() => {
        if (!open) {
            return undefined;
        }

        // A press anywhere else closes it, as on a live stream's tag.
        const away = (event: PointerEvent) => {
            const target = event.target as Node | null;
            if (target && (chip.current?.contains(target) || document.getElementById(profileId)?.contains(target))) {
                return;
            }

            setOpen(null);
        };
        const onKey = (event: KeyboardEvent) => {
            if (event.key === 'Escape') {
                event.stopPropagation();
                setOpen(null);
                chip.current?.focus();

                return;
            }

            const profile = document.getElementById(profileId);
            const active = document.activeElement;
            if (event.key !== 'Tab' || event.ctrlKey || event.altKey || event.metaKey || !profile || !active || !profile.contains(active)) {
                return;
            }

            const inside = focusStops(profile);
            const first = inside[0];
            const last = inside[inside.length - 1];
            if (event.shiftKey && (active === profile || active === first || inside.length === 0)) {
                event.preventDefault();
                event.stopPropagation();
                setOpen(null);
                chip.current?.focus();
            } else if (!event.shiftKey && (active === last || inside.length === 0)) {
                // What follows the button in the dialog, the profile's own
                // stops aside; round to the first, as the trap would, when
                // the button is the dialog's last.
                const box = chip.current?.closest<HTMLElement>('[aria-modal="true"]');
                const stops = box ? focusStops(box).filter((stop) => !profile.contains(stop)) : [];
                const at = chip.current ? stops.indexOf(chip.current) : -1;
                const next = at >= 0 ? stops[(at + 1) % stops.length] : null;
                event.preventDefault();
                event.stopPropagation();
                setOpen(null);
                (next || chip.current)?.focus();
            }
        };

        document.addEventListener('pointerdown', away, true);
        window.addEventListener('keydown', onKey, true);

        return () => {
            document.removeEventListener('pointerdown', away, true);
            window.removeEventListener('keydown', onKey, true);
        };
    }, [open, profileId]);

    if (!tag || tagTitleOf(tag) === '') {
        return null;
    }

    const line = tagLine(tag);
    const words = (
        <>
            <CategoryIcon
                category={tag.category}
                className='vntv-onnow-kind'
            />
            <span className='vntv-onnow-title'>{line}</span>
        </>
    );

    if (!tag.media) {
        return (
            <p className='vntv-clipview-tag'>
                <span className='vntv-onnow vntv-clipview-tag-chip'>{words}</span>
            </p>
        );
    }

    return (
        <p className='vntv-clipview-tag'>
            <button
                ref={chip}
                type='button'
                className='vntv-onnow vntv-clipview-tag-chip'
                data-media='true'
                data-open={open ? 'true' : undefined}
                aria-haspopup='dialog'
                aria-expanded={Boolean(open)}
                aria-controls={open ? profileId : undefined}
                aria-label={`Clipped during ${line}. Show what it is`}
                onClick={(event) => {
                    // detail is 0 for a press made with the keyboard.
                    setOpen((was) => (was ? null : {keyboard: event.detail === 0}));
                }}
                onBlur={(event) => {
                    const next = event.relatedTarget as Node | null;
                    if (next && !document.getElementById(profileId)?.contains(next)) {
                        setOpen(null);
                    }
                }}
            >{words}</button>

            {open && chip.current ? (
                <MediaProfile
                    key={tag.media.ref}
                    id={profileId}
                    media={tag.media}
                    anchor={chip.current}
                    context={`${who} · when it was clipped`}
                    layer='modal'
                    container={chip.current.closest('[aria-modal="true"]')}
                    onClose={() => {
                        setOpen(null);
                        chip.current?.focus();
                    }}
                    onFocusLeave={(next) => {
                        if (next && !chip.current?.contains(next)) {
                            setOpen(null);
                        }
                    }}
                    focusInside={open.keyboard}
                />
            ) : null}
        </p>
    );
};

type Props = {
    id: string;
    me: Me;

    // Who holds Best Clipper, for the badge beside the clipper's name.
    bestClipperId?: string;

    // snapshot.clips.version: the viewer asks about its clip again whenever
    // it changes, so a clip that has gone says so.
    version?: string;
    now: number;
    onBack: () => void;
};

// Why the clip's file would not load, asked of the server: the clip itself,
// fetched again through clips.ts, which locks the dialog by itself when the
// reader is no longer linked and forgets a clip that has gone (the viewer
// then says so). Fetched fine, it was the file: the browser's own decoding,
// when that is what failed, or else a failure worth trying again.
function explainFailure(id: string, mediaError: number): Promise<string> {
    return loadClip(id).then(() => (
        mediaError === MEDIA_DECODE_FAILED ? CLIP_CANNOT_PLAY : FAILED_TO_LOAD
    ), (reason: unknown) => clipErrorCopy(clipErrorCodeOf(reason), 'clip', {brand: nameNow()}, clipErrorTextOf(reason)).text);
}

// MediaError.MEDIA_ERR_DECODE: the file came, and this browser could not
// decode it.
const MEDIA_DECODE_FAILED = 3;

export const ClipViewer: React.FC<Props> = ({id, me, bestClipperId, version, now, onBack}) => {
    const clip = useClip(id);
    const [status, setStatus] = React.useState<'loading' | 'ready' | 'gone' | 'error'>(() => (getClip(id) ? 'ready' : 'loading'));
    const [loadError, setLoadError] = React.useState('');
    const [attempt, setAttempt] = React.useState(0);

    React.useEffect(() => {
        let current = true;
        loadClip(id).then((clip) => {
            if (current && clip) {
                setStatus('ready');
            }
        }).catch((reason: unknown) => {
            if (!current) {
                return;
            }

            const code = clipErrorCodeOf(reason);
            if (code === 'clip_gone') {
                setStatus('gone');
            } else if (!isAccessRefusal(code) && !getClip(id)) {
                // Refused for access: the page reads "me" again and follows
                // it, and the dialog goes with the lock.
                setStatus('error');
                setLoadError(clipErrorCopy(code, 'clip', {brand: nameNow()}, clipErrorTextOf(reason)).text);
            }
        });

        return () => {
            current = false;
        };
    }, [id, version, attempt]);

    const twice = useTwoClicks();
    const share = useShareClip(id);
    const access = useAccess();
    const linked = access === 'linked';
    const [busy, setBusy] = React.useState('');
    const [said, setSaid] = React.useState('');
    const [failure, setFailure] = React.useState('');

    // What Download last said, on the player's own line under the picture.
    // Whatever is pressed next takes its place, as one line of words did for
    // all three before 3.3.0.
    const [playerSays, setPlayerSays] = React.useState<PlayerSays | null>(null);
    const alive = React.useRef(true);
    React.useEffect(() => () => {
        alive.current = false;
    }, []);

    const gone = status === 'gone' || (status === 'ready' && !clip);

    // Back is always there, so it is where focus goes from a button that is
    // taken away as it is pressed.
    const back = React.useRef<HTMLButtonElement>(null);
    const explain = React.useCallback((mediaError: number) => explainFailure(id, mediaError), [id]);

    // A pill in the row of actions can also be taken away with nobody
    // pressing it: Delete or Report, when the viewer asks about its clip
    // again and the server's flags for it have changed, or the whole row,
    // once the clip has gone. Focus on it would fall to the page's body, and
    // the next Tab would start again from the top of the dialog; instead it
    // goes to the pill that took its place (actionAfterLoss), and to Back
    // once there is none. Download, which goes the same way when a switch
    // turns downloads off, is in the player, which keeps focus itself
    // (ClipTile.tsx).
    const actions = React.useRef<HTMLDivElement>(null);
    const actionAt = React.useRef(0);
    const actionsNow = (): HTMLElement[] => Array.from(actions.current?.children || []).filter(
        (element): element is HTMLElement => element instanceof HTMLButtonElement,
    );
    const keepFocus = useFocusHome(() => {
        const left = actionsNow();

        return left[actionAfterLoss(actionAt.current, left.length)] || back.current;
    });
    const actionFocused = (event: React.FocusEvent<HTMLElement>) => {
        keepFocus.onFocus(event);
        actionAt.current = Math.max(0, actionsNow().indexOf(event.target));
    };

    let body: React.ReactNode;
    if (gone) {
        body = (
            <p
                className='vntv-clipview-gone'
                role='status'
            >{CARD_GONE}</p>
        );
    } else if (!clip && status === 'error') {
        body = (
            <div
                className='vntv-clipview-gone'
                role='alert'
            >
                <p>{loadError}</p>
                <button
                    type='button'
                    className='vntv-pill'
                    onClick={(event) => {
                        if (document.activeElement === event.currentTarget) {
                            back.current?.focus({preventScroll: true});
                        }
                        setStatus('loading');
                        setAttempt((value) => value + 1);
                    }}
                >{'Try again'}</button>
            </div>
        );
    } else if (!clip) {
        // The player's empty frame, the same shape as it will be, so the
        // dialog does not change size when the clip arrives.
        body = (
            <>
                <div
                    className='vntv-player vntv-cliptile'
                    data-state='loading'
                    aria-hidden={true}
                >
                    <Screen noise={false}/>
                    <div className='vntv-player-bar'/>
                </div>
                <p
                    className='vntv-clipview-meta'
                    role='status'
                >{'Loading the clip…'}</p>
            </>
        );
    } else {
        const shown = shownTitleOf(clip);
        const reported = clip.reported;

        const report = (event: React.MouseEvent) => {
            if (reported || busy !== '' || !twice.press('report', event)) {
                return;
            }

            setBusy('report');
            setSaid('');
            setFailure('');
            setPlayerSays(null);
            reportOn(clip.id).then(() => {
                if (alive.current) {
                    setSaid('Reported. The admins will take a look.');
                }
            }).catch((reason: unknown) => {
                if (alive.current) {
                    setFailure(clipErrorCopy(clipErrorCodeOf(reason), 'report', {brand: nameNow()}, clipErrorTextOf(reason)).text);
                }
            }).then(() => {
                if (alive.current) {
                    setBusy('');
                }
            });
        };

        const remove = (event: React.MouseEvent) => {
            if (busy !== '' || !twice.press('delete', event)) {
                return;
            }

            setBusy('delete');
            setSaid('');
            setFailure('');
            setPlayerSays(null);
            deleteOn(clip.id).then(() => {
                if (alive.current) {
                    onBack();
                }
            }).catch((reason: unknown) => {
                if (alive.current) {
                    setBusy('');
                    setFailure(clipErrorCopy(clipErrorCodeOf(reason), 'delete', {brand: nameNow()}, clipErrorTextOf(reason)).text);
                }
            });
        };

        // Download (3.2.1), pressed in the player: a ticket from the server,
        // then the browser saves the file from the address it answered, so a
        // five-minute clip never sits in the page. Said under the picture
        // once it has started; the browser shows the rest.
        const download = () => {
            if (busy !== '') {
                return;
            }

            twice.disarm();
            setBusy('download');
            setSaid('');
            setFailure('');
            setPlayerSays(null);
            downloadClip(clip.id).then(() => {
                if (alive.current) {
                    setPlayerSays({text: DOWNLOAD_STARTED, error: false});
                }
            }).catch((reason: unknown) => {
                if (alive.current) {
                    setPlayerSays({text: clipErrorCopy(clipErrorCodeOf(reason), 'download', {brand: nameNow()}, clipErrorTextOf(reason)).text, error: true});
                }
            }).then(() => {
                if (alive.current) {
                    setBusy('');
                }
            });
        };

        let reportWords = 'Report';
        if (reported) {
            reportWords = 'Reported';
        } else if (busy === 'report') {
            reportWords = 'Reporting…';
        } else if (twice.armed === 'report') {
            reportWords = 'Click again to report this clip to the admins';
        }

        let deleteWords = 'Delete';
        if (busy === 'delete') {
            deleteWords = 'Deleting…';
        } else if (twice.armed === 'delete') {
            deleteWords = deleteConfirm(clip, me.isAdmin);
        }

        let armedWords = '';
        if (twice.armed === 'report') {
            armedWords = `${reportWords}.`;
        } else if (twice.armed === 'delete') {
            armedWords = `${deleteWords}.`;
        }

        body = (
            <>
                <ClipTile
                    key={clip.id}
                    clip={clip}
                    explain={explain}
                    download={clipDownloadOffered(clip, access) ? {
                        preparing: busy === 'download',
                        disabled: busy !== '',
                        press: download,
                    } : undefined}
                    says={playerSays}
                />

                <div className='vntv-clipview-head'>
                    <h3
                        className='vntv-clipview-title'
                        data-untitled={clip.title === '' ? 'true' : undefined}
                    >{shown}</h3>
                    <UpvotePill clip={clip}/>
                </div>

                <ClipTagLine
                    tag={clip.tag}
                    who={streamLabel(clip.streamer)}
                />

                <p className='vntv-clipview-meta vntv-clipview-people'>
                    {'Clipped by '}
                    <ClipName
                        person={clip.clipper}
                        best={Boolean(bestClipperId) && clip.clipper.userId === bestClipperId}
                    />
                    {' from '}
                    <ClipName
                        person={clip.streamer}
                        label={streamLabel(clip.streamer)}
                    />
                </p>
                <p className='vntv-clipview-meta'>
                    {`${clipTime(clip.durationMs)} · ${agoWords(clip.createdAt, now)} · ${goesWords(clip.expiresAt, now)}`}
                </p>
                {clip.streamNote ? <p className='vntv-clipview-note'>{clip.streamNote}</p> : null}

                {/* Share first: anybody linked who can see a clip here may
                    share it (addendum 4.10), and Report and Delete come
                    after. Download is not here but in the player (3.3.0).
                    Somebody let in to watch only has none of them: the
                    server says so in the clip's own flags, and Share is left
                    out whenever canShare is false. */}
                <div
                    ref={actions}
                    className='vntv-clipview-actions'
                    onFocus={actionFocused}
                    onBlur={keepFocus.onBlur}
                >
                    {clip.canShare !== false && linked ? (
                        <ShareClipButton
                            share={share}
                            className='vntv-pill'
                        />
                    ) : null}
                    {clip.canReport && (
                        <button
                            type='button'
                            className='vntv-pill vntv-pill-muted'
                            aria-disabled={reported || busy !== '' ? true : undefined}
                            data-armed={twice.armed === 'report' ? 'true' : undefined}
                            onClick={report}
                        >{reportWords}</button>
                    )}
                    {clip.canDelete && (
                        <button
                            type='button'
                            className='vntv-pill vntv-pill-danger'
                            aria-disabled={busy !== '' ? true : undefined}
                            data-armed={twice.armed === 'delete' ? 'true' : undefined}
                            onClick={remove}
                        >{deleteWords}</button>
                    )}
                </div>
                <ShareClipStatus share={share}/>

                {/* What Report's or Delete's first press changed it to, read
                    out, as Share's is: heard, not seen. */}
                <p
                    className='vntv-sr-only'
                    role='status'
                >{armedWords}</p>

                {/* Always there, so what is put in them is announced. */}
                <p
                    className='vntv-clipview-status'
                    role='status'
                >{said}</p>
                <p
                    className='vntv-clipview-status vntv-notice-error'
                    role='alert'
                >{failure}</p>
            </>
        );
    }

    return (
        <div className='vntv-clipview'>
            <button
                ref={back}
                type='button'
                className='vntv-clipview-back'
                onClick={onBack}
            >{'‹ All clips'}</button>
            {body}
        </div>
    );
};
