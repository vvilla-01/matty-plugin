import React from 'react';

import {
    STREAMER_DOWNLOADS_OFF,
    STREAMER_DOWNLOADS_OFF_DONE,
    STREAMER_DOWNLOADS_ON,
    STREAMER_DOWNLOADS_ON_DONE,
    STREAMER_DOWNLOADS_SWITCH,
    STREAMER_OFF_DONE,
    STREAMER_ON_DONE,
    STREAMER_SWITCH,
    WIPE_CONFIRM,
    WIPE_DONE,
    clipErrorCopy,
    ofMyStreamLine,
    streamerDownloadsTitle,
    streamerSwitchTitle,
} from '../clipLogic';
import type {StreamerView} from '../clipLogic';
import {allowClipsOfMine, allowDownloadsOfMine, clipErrorCodeOf, clipErrorTextOf, openClips, useClipsOfMine, wipeMine} from '../clips';
import {useFocusHome} from '../focusHome';
import {nameNow, useName} from '../name';
import {useTwoClicks} from '../twoClicks';

// A streamer's say over clips of their own stream, in the sheet the scissors
// in the footer's bar open (spec 3.11, with the addendum's 3.5 and 3.8).
// Recording is on for every live stream (Victor's rule 3), so whoever has
// linked theirs is told so, and given the switch to stop it and a way to wipe
// what is held:
//
//   - "Allow clips of my stream", a switch, first, as a settings row: its
//     words on the left and the switch on the right. Off wipes what is held and every
//     draft cut from it, whoever made it, and it stays off with the stream —
//     unlinking does not switch it back on (the tooltip says so);
//   - the notice under it, which describes it: what is kept, and for how
//     long a stretch;
//   - Wipe the last 2:00, two presses, while there is something held: the
//     recording while they are live, or the end of it for the ten minutes
//     after it stops, which the notice then says. Published clips are not
//     touched;
//   - how many clips of their stream the last list had, and a way to them;
//   - from 3.2.1, while an admin allows downloads at all, a second switch,
//     "Let people download clips of my stream" (design/3.2.1.md 1.3), on
//     until they switch it off. It reaches every clip of the stream, those
//     already made and every one to come, and it deletes nothing. While
//     Clips are off — switched off, or with nowhere to keep anything — clips
//     already made can still be downloaded, so the sheet stays, with this
//     switch alone and the clips line (streamerView's 'downloads').
//
// What the footer shows is worked out in clipLogic.ts (streamerView), from
// the snapshot and from what the server said in "me": nothing where it said
// nothing. Somebody who has not linked a stream (let in to watch only) gets
// only a notice when one matched to them by their username is being kept,
// with no switch — the server would refuse it — which the panel's watch-only
// footer shows itself (Sidebar.tsx).

// How long a result stays said under the controls once it has been read out.
const SAID_MS = 20000;

// Ids for the notice the switch is described by, from a counter; see
// TitleSearch.
let nextNoticeId = 0;

type Props = {
    view: StreamerView;
    lengthSeconds: number;

    // The stream whose recording, or held end, Wipe acts on; '' for none.
    handle: string;

    // The server said what the switch is now: the panel takes it into "me",
    // and asks for the streams again, whose state follows.
    onAllowed: (allowed: boolean) => void;
    onWiped: () => void;

    // The same for the downloads switch.
    onDownloads: (downloads: boolean) => void;
};

export const StreamerClips: React.FC<Props> = ({view, lengthSeconds, handle, onAllowed, onWiped, onDownloads}) => {
    const mine = useClipsOfMine();
    const twice = useTwoClicks();
    const name = useName();
    const [noticeId] = React.useState(() => {
        nextNoticeId += 1;

        return `villanet-clipnotice-${nextNoticeId}`;
    });
    const [downloadsNoticeId] = React.useState(() => {
        nextNoticeId += 1;

        return `villanet-clipnotice-${nextNoticeId}`;
    });
    const [busy, setBusy] = React.useState<'' | 'allow' | 'wipe' | 'downloads'>('');

    // The position asked for, shown at once while the server is asked, and
    // dropped once it has answered, either way: one for each switch.
    const [asking, setAsking] = React.useState<boolean | null>(null);
    const [askingDownloads, setAskingDownloads] = React.useState<boolean | null>(null);
    const [note, setNote] = React.useState('');
    const [error, setError] = React.useState('');
    const alive = React.useRef(true);
    React.useEffect(() => () => {
        alive.current = false;
    }, []);

    // A result is read out as it arrives, and then let go, so the footer does
    // not go on carrying old news: a failure as well as a success.
    React.useEffect(() => {
        if (note === '' && error === '') {
            return undefined;
        }

        const timer = window.setTimeout(() => {
            setNote('');
            setError('');
        }, SAID_MS);

        return () => window.clearTimeout(timer);
    }, [note, error]);

    // And a failure is old news as soon as the server's own position moves
    // under it — switched from another tab, or a refetched "me".
    let position: string = view.kind;
    if (view.kind === 'streamer') {
        position = `${String(view.allowed)}|${String(view.downloads)}`;
    } else if (view.kind === 'downloads') {
        position = `downloads|${String(view.downloads)}`;
    }
    React.useEffect(() => {
        setError('');
    }, [position]);

    // Wipe goes once there is nothing left to wipe — wiped, the end of a
    // stream that has stopped let go, the recording stopped — and the clips
    // line once the list has none: focus that was on either goes to the
    // switch rather than out of the panel. Only focus that went with the
    // button, never focus somebody has since moved elsewhere (focusHome.ts).
    // The switch is the first one in the sheet: clipping's, or the downloads
    // switch while it is there alone, so clipping's switch going with Clips
    // switched off hands focus to it. All of this going at once, with no
    // downloads to switch either, is the footer's to catch (Sidebar.tsx).
    const switchRef = React.useRef<HTMLButtonElement | null>(null);
    const keepFocus = useFocusHome(() => switchRef.current);
    const wipeShown = view.kind === 'streamer' && view.wipe && handle !== '';

    if (view.kind === 'attributed') {
        return <p className='vntv-clips-attributed'>{view.notice}</p>;
    }

    // Clipping's part of the sheet, while Clips are on.
    const clipping = view.kind === 'streamer' ? view : null;
    const allowed = asking === null ? clipping?.allowed === true : asking;
    const downloads = askingDownloads === null ? view.downloads : askingDownloads;

    const flip = () => {
        if (busy !== '') {
            return;
        }

        const next = !allowed;
        twice.disarm();
        setBusy('allow');
        setAsking(next);
        setNote('');
        setError('');
        allowClipsOfMine(next).then((now) => {
            if (!alive.current) {
                return;
            }
            setNote(now ? STREAMER_ON_DONE : STREAMER_OFF_DONE);
            onAllowed(now);
        }, (reason: unknown) => {
            if (alive.current) {
                setError(clipErrorCopy(clipErrorCodeOf(reason), 'allow', {lengthSeconds, brand: nameNow()}, clipErrorTextOf(reason)).text);
            }
        }).then(() => {
            if (alive.current) {
                setBusy('');
                setAsking(null);
            }
        });
    };

    const flipDownloads = () => {
        if (busy !== '' || downloads === undefined) {
            return;
        }

        const next = !downloads;
        twice.disarm();
        setBusy('downloads');
        setAskingDownloads(next);
        setNote('');
        setError('');
        allowDownloadsOfMine(next).then((now) => {
            if (!alive.current) {
                return;
            }
            setNote(now ? STREAMER_DOWNLOADS_ON_DONE : STREAMER_DOWNLOADS_OFF_DONE);
            onDownloads(now);
        }, (reason: unknown) => {
            if (alive.current) {
                setError(clipErrorCopy(clipErrorCodeOf(reason), 'downloads', {lengthSeconds, brand: nameNow()}, clipErrorTextOf(reason)).text);
            }
        }).then(() => {
            if (alive.current) {
                setBusy('');
                setAskingDownloads(null);
            }
        });
    };

    const wipe = (event: React.MouseEvent<HTMLButtonElement>) => {
        if (busy !== '' || !twice.press('wipe', event)) {
            return;
        }

        setBusy('wipe');
        setNote('');
        setError('');
        wipeMine(handle).then(() => {
            if (!alive.current) {
                return;
            }
            setNote(WIPE_DONE);
            onWiped();
        }, (reason: unknown) => {
            if (alive.current) {
                setError(clipErrorCopy(clipErrorCodeOf(reason), 'wipe', {lengthSeconds, brand: nameNow()}, clipErrorTextOf(reason)).text);
            }
        }).then(() => {
            if (alive.current) {
                setBusy('');
            }
        });
    };

    let wipeWords = clipping?.wipeWords || '';
    if (busy === 'wipe') {
        wipeWords = 'Wiping…';
    } else if (twice.armed === 'wipe') {
        wipeWords = WIPE_CONFIRM;
    }

    return (
        <div
            className='vntv-clips-streamer'
            role='group'
            aria-label='Clips of your stream'
            onFocus={keepFocus.onFocus}
            onBlur={keepFocus.onBlur}
        >
            {/* A switch, so it says on or off rather than what pressing it
                would do; its name stays the same either way. Described by
                the notice under it, which says what on means just now. */}
            {clipping ? (
                <>
                    <button
                        ref={switchRef}
                        type='button'
                        role='switch'
                        className='vntv-switch'
                        aria-checked={allowed}
                        aria-disabled={busy !== '' ? true : undefined}
                        aria-describedby={noticeId}
                        title={streamerSwitchTitle(lengthSeconds)}
                        onClick={flip}
                    >
                        <span className='vntv-switch-label'>{STREAMER_SWITCH}</span>
                        <span
                            className='vntv-switch-track'
                            aria-hidden={true}
                        ><span className='vntv-switch-thumb'/></span>
                    </button>

                    <p
                        id={noticeId}
                        className='vntv-clips-streamer-notice'
                    >{clipping.notice}</p>
                </>
            ) : null}

            {/* The second switch, only while an admin allows downloads:
                the same settings row, described by its own notice, and set
                apart from the first one's when that is there. */}
            {downloads === undefined ? null : (
                <div className={clipping ? 'vntv-clips-streamer-downloads' : undefined}>
                    <button
                        ref={clipping ? undefined : switchRef}
                        type='button'
                        role='switch'
                        className='vntv-switch'
                        aria-checked={downloads}
                        aria-disabled={busy !== '' ? true : undefined}
                        aria-describedby={downloadsNoticeId}
                        title={streamerDownloadsTitle(name)}
                        onClick={flipDownloads}
                    >
                        <span className='vntv-switch-label'>{STREAMER_DOWNLOADS_SWITCH}</span>
                        <span
                            className='vntv-switch-track'
                            aria-hidden={true}
                        ><span className='vntv-switch-thumb'/></span>
                    </button>
                    <p
                        id={downloadsNoticeId}
                        className='vntv-clips-streamer-notice'
                    >{downloads ? STREAMER_DOWNLOADS_ON : STREAMER_DOWNLOADS_OFF}</p>
                </div>
            )}

            {wipeShown || mine > 0 ? (
                <div className='vntv-clips-streamer-controls'>
                    {wipeShown ? (
                        <button
                            type='button'
                            className='vntv-clips-streamer-wipe'
                            data-armed={twice.armed === 'wipe' ? 'true' : undefined}
                            aria-disabled={busy !== '' ? true : undefined}
                            title='Throw away what this server holds of your stream right now, and every draft cut from it. Published clips stay.'
                            onClick={wipe}
                        >{wipeWords}</button>
                    ) : null}

                    {mine > 0 ? (
                        <span className='vntv-clips-streamer-mine'>
                            {`${ofMyStreamLine(mine)} · `}
                            <button
                                type='button'
                                className='vntv-clips-streamer-view'
                                aria-haspopup='dialog'
                                aria-label={`View the ${ofMyStreamLine(mine)}`}
                                onClick={() => openClips(undefined, 'yours')}
                            >{'View'}</button>
                        </span>
                    ) : null}
                </div>
            ) : null}

            <p
                className='vntv-clips-streamer-said'
                role='status'
            >{note}</p>
            <p
                className='vntv-notice-error vntv-clips-streamer-said'
                role='alert'
            >{error}</p>

            {/* What Wipe's first press changed it to, read out: a change of
                the focused button's name is not spoken by every screen
                reader, and the press would otherwise seem to do nothing.
                Heard, not seen, as the editor's Discard. */}
            <p
                className='vntv-sr-only'
                role='status'
            >{twice.armed === 'wipe' && busy === '' ? `${WIPE_CONFIRM}.` : ''}</p>
        </div>
    );
};
