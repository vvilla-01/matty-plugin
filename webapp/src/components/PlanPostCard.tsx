import React from 'react';

import {windowKind} from '../away';
import {joinPlan, mediaImageURL} from '../client';
import {accessNow, useAccess, useIdentity} from '../identity';
import {mayWatch} from '../lockLogic';
import {openLock} from '../lockPending';
import {useName} from '../name';
import {planCardView, planNoPanel} from '../planLogic';
import type {PlanCardView, PlanReader} from '../planLogic';
import {getSnapshot, snapshotAge, useSnapshot} from '../store';
import {viewerZone} from '../time';
import {watchOrOpen} from '../twitch';
import {liveHandlesOf} from '../twitchLogic';
import {markOpenedByHand} from '../watching';

import {Avatar} from './Avatar';
import {CategoryIcon} from './categories';
import {PlayIcon, ScheduleIcon} from './icons';
import {Backdrop} from './LiveCard';

// A shared plan, as a post in a channel shows it (design/3.2.0.md 6.5): the
// card Mattermost draws in place of the message of every post of the plugin's
// plan type, wherever the post appears. Built on the shared clip's card
// (ClipPostCard.tsx), and like it in Mattermost's theme's colours, not the
// panel's: a black card in a white channel would shout. The plugin red is for
// LIVE and the eyebrow's mark.
//
// What it says is planLogic.ts's planCardView: the post carries only the
// plan's id and start, and everything else comes from the snapshot every
// reader already has, while the plan is listed. Nobody's card asks the
// server for anything.
//
//   - Before it starts: the title, the host and who is in, the day and time
//     in the reader's own zone with how far off it is, and Join for a linked
//     streamer who is not the host (today's Join: "I'm streaming in this
//     too"). In ✓ to leave; "Your plan" for the host; Full at twelve.
//   - Once its time has come and the host, or somebody in it, is on the air:
//     LIVE and Watch, which opens their stream in the panel, or for a locked
//     reader the lock with the stream waiting behind it
//     (design/lock-screen.md 7).
//   - Cancelled, or been and gone: dimmed, and no title or name at all.
//
// Drawn as the banner (Victor's P5 pick): the title's art at 2:1 across the
// top of the card, with LIVE on it, and the words under it. A plan with no
// art of its own has no banner, and the card is then words only, so its
// height varies by plan. A locked reader sees the card in full (6.2, an
// approved departure from 12.1), as plans are already shown to everybody in
// profile popovers; Watch opens the lock for them.
//
// Clicks. Every control is a real button and no svg takes the pointer, as on
// the clip card: inside a permalink preview, a click on anything else
// follows the permalink.

type Props = {
    post: {id?: string; type?: string; message?: string; props?: unknown};
    compactDisplay?: boolean;
    isRHS?: boolean;
    theme?: unknown;
};

function messageOf(post: unknown): string {
    try {
        const message = (post as {message?: unknown} | null)?.message;

        return typeof message === 'string' ? message : '';
    } catch {
        return '';
    }
}

// As the clip card's: a card that fails shows the post's own message, as
// plain text, and tries again when the post changes.
class PlanCardBoundary extends React.Component<{post: unknown; children: React.ReactNode}, {failed: boolean}> {
    state = {failed: false};

    static getDerivedStateFromError() {
        return {failed: true};
    }

    componentDidUpdate(previous: {post: unknown}) {
        if (this.state.failed && previous.post !== this.props.post) {
            this.setState({failed: false});
        }
    }

    render() {
        if (this.state.failed) {
            return <p className='vntv-postclip-fallback'>{messageOf(this.props.post)}</p>;
        }

        return this.props.children;
    }
}

// The banner: the title's art, through the open picture route, which serves
// it while the plan is listed (media-lookups.md refShownHere). One that fails
// to load leaves the host's face blurred in its place, so the card does not
// change height when it fails.
const Art: React.FC<{view: PlanCardView}> = ({view}) => {
    const url = view.art && view.title ? mediaImageURL(view.art, 'backdrop') : '';
    const [failed, setFailed] = React.useState(false);

    React.useEffect(() => setFailed(false), [url]);

    if (url !== '' && !failed) {
        return (
            <img
                className='vntv-plancard-img'
                src={url}
                alt=''
                loading='lazy'
                draggable={false}
                onError={() => setFailed(true)}
            />
        );
    }

    return <Backdrop userId={view.host?.userId}/>;
};

// The host's face and the first three who are in, tucked behind it, and
// their names: "Dave with Bob and Erin".
const Who: React.FC<{view: PlanCardView}> = ({view}) => {
    if (!view.host) {
        return null;
    }

    return (
        <p className='vntv-plancard-who'>
            <span
                className='vntv-plancard-faces'
                aria-hidden={true}
            >
                {[view.host, ...view.joined.slice(0, 3)].map((person, index) => (
                    <Avatar
                        key={`${person.userId || person.who}-${index}`}
                        className='vntv-plancard-face'
                        userId={person.userId}
                        name={person.who}
                        decorative={true}
                    />
                ))}
            </span>
            <span className='vntv-plancard-names'>
                <b>{view.host.who}</b>
                {view.company ? ` ${view.company}` : ''}
            </span>
        </p>
    );
};

type FaceProps = {
    view: PlanCardView;
    compact?: boolean;

    // A window with no side panel: Watch has nowhere to open.
    noPanel?: boolean;
    busy?: boolean;
    note?: string;
    onWatch?: () => void;
    onJoin?: (join: boolean) => void;
};

// The card itself, for a view: what the post type's component draws, and
// what the lab lays out side by side.
export const PlanCardFace: React.FC<FaceProps> = ({view, compact, noPanel, busy, note, onWatch, onJoin}) => {
    const name = useName();
    if (view.state === 'fallback') {
        return <p className='vntv-postclip-fallback'>{view.message}</p>;
    }

    const live = view.state === 'live';
    const gone = view.state === 'ended' || view.state === 'cancelled';
    const titled = Boolean(view.title);

    // The banner only for a plan with art of its own: a host's face blurred
    // across the card's whole width is a smear, not a picture. None in
    // compact display (6.5).
    const art = !compact && titled && Boolean(view.art);
    const watchHere = Boolean(view.watch) && !noPanel;

    // When: the day and the time, and how far off, while it is to come.
    // Nothing once it is on (LIVE and Watch say it) or gone.
    const when = !live && !gone ? `${view.day}, ${view.clock}` : '';

    const join = view.join;
    let joinButton: React.ReactNode = null;
    if (join?.kind === 'mine') {
        joinButton = <span className='vntv-plancard-mine'>{join.words}</span>;
    } else if (join) {
        joinButton = (
            <button
                type='button'
                className='vntv-plancard-join'
                data-primary={!view.watch && join.kind === 'join' ? 'true' : undefined}
                aria-pressed={join.kind === 'in' ? true : undefined}
                aria-label={join.label}
                aria-disabled={join.kind === 'full' || busy ? true : undefined}
                title={join.title}
                onClick={() => {
                    if (join.kind !== 'full' && !busy) {
                        onJoin?.(join.kind === 'join');
                    }
                }}
            >{join.words}</button>
        );
    }

    const actions = view.watch || joinButton ? (
        <div className='vntv-plancard-actions'>
            {view.watch ? (
                <button
                    type='button'
                    className='vntv-postclip-watch'
                    aria-label={view.watch.label}
                    aria-disabled={noPanel ? true : undefined}
                    onClick={watchHere ? onWatch : undefined}
                >
                    <PlayIcon size={12}/>
                    {'Watch'}
                </button>
            ) : null}
            {joinButton}
        </div>
    ) : null;

    let sentence: React.ReactNode = null;
    if (view.sentence) {
        sentence = <p className={gone ? 'vntv-plancard-gone' : 'vntv-plancard-waiting'}>{view.sentence}</p>;
    }

    // The card is its own container (styles.ts), so what it draws is laid
    // out one level in: a container query cannot change the container itself.
    return (
        <div
            className='vntv vntv-plancard'
            role='group'
            aria-label={view.label}
            data-state={view.state}
            data-dimmed={view.dimmed ? 'true' : undefined}
            data-compact={compact ? 'true' : undefined}
            data-art={art ? 'true' : undefined}
        >
            <div className='vntv-plancard-in'>
                {art ? (

                    // The picture does what Watch does, for the mouse; to the
                    // keyboard and a screen reader Watch is the one way.
                    <button
                        type='button'
                        className='vntv-plancard-art'
                        data-blank={titled ? undefined : 'true'}
                        tabIndex={-1}
                        aria-hidden={true}
                        onClick={watchHere ? onWatch : undefined}
                    >
                        <Art view={view}/>
                        {live ? <span className='vntv-plancard-live'>{'Live'}</span> : null}
                    </button>
                ) : null}

                <div className='vntv-plancard-body'>
                    {/* LIVE after the eyebrow where there is no banner to
                        carry it. */}
                    <p className='vntv-plancard-eyebrow'>
                        <i
                            className='vntv-plancard-icon'
                            aria-hidden={true}
                        ><ScheduleIcon size={12}/></i>
                        {`${name} plan`}
                        {live && !art ? <span className='vntv-plancard-live vntv-plancard-live-inline'>{'Live'}</span> : null}
                    </p>

                    {titled ? (
                        <p className='vntv-plancard-title'>
                            <CategoryIcon
                                category={view.category}
                                className='vntv-plancard-kind'
                                size={13}
                            />
                            <span
                                className='vntv-plancard-name'
                                title={view.title}
                            >{view.title}</span>
                        </p>
                    ) : null}

                    {sentence}

                    <Who view={view}/>

                    {when !== '' ? (
                        <p className='vntv-plancard-when'>
                            {when}
                            {view.lede ? ' · ' : ''}
                            {view.lede ? <b className='vntv-plancard-lede'>{view.lede}</b> : null}
                        </p>
                    ) : null}

                    {actions}
                    {view.watch && noPanel ? <p className='vntv-postclip-note'>{planNoPanel(name)}</p> : null}

                    {/* The answer to a Join that did not go: always there, so
                        what is put in it is announced. */}
                    <p
                        className='vntv-postclip-note vntv-plancard-answer'
                        role='status'
                    >{note || ''}</p>
                </div>
            </div>
        </div>
    );
};

const NOTE_MS = 8000;

// The post type's component: who is reading, the snapshot, and the clock,
// made into a view, and the two presses.
const PlanCard: React.FC<Props> = ({post, compactDisplay}) => {
    const me = useIdentity();
    const access = useAccess();
    const snapshot = useSnapshot();
    const name = useName();
    const [now, setNow] = React.useState(() => Date.now());
    const [busy, setBusy] = React.useState(false);
    const [note, setNote] = React.useState('');
    const alive = React.useRef(true);

    React.useEffect(() => () => {
        alive.current = false;
    }, []);

    // "in 40 min" moves; and a plan's start passing turns it to Due now.
    React.useEffect(() => {
        const timer = window.setInterval(() => setNow(Date.now()), 30000);

        return () => window.clearInterval(timer);
    }, []);

    React.useEffect(() => {
        if (note === '') {
            return undefined;
        }

        const timer = window.setTimeout(() => setNote(''), NOTE_MS);

        return () => window.clearTimeout(timer);
    }, [note]);

    const liveHandles = React.useMemo(() => liveHandlesOf(snapshot.people), [snapshot.people]);
    const reader: PlanReader = access === 'unknown' ? undefined : access;
    const view = planCardView({
        post,
        schedule: snapshotAge() === Infinity ? null : snapshot.schedule || [],
        liveHandles,
        reader,
        meUserId: me?.userId,
        now,
        zone: viewerZone(),
        name,
    });

    // Watch, as the profile popover's: the stream added and the panel
    // opened, or for a reader who may not watch the lock, naming it.
    const watch = () => {
        const target = view.watch;
        const stream = target ? getSnapshot().streams.find((candidate) => candidate.handle === target.handle) : undefined;
        if (!target || !stream) {
            return;
        }
        if (!mayWatch(accessNow())) {
            openLock({kind: 'stream', handle: stream.handle, name: target.who});

            return;
        }

        markOpenedByHand();
        watchOrOpen(stream, 'add');
    };

    const join = (on: boolean) => {
        setBusy(true);
        setNote('');
        joinPlan(view.id, on).catch((reason: unknown) => {
            if (alive.current) {
                setNote(reason instanceof Error ? reason.message : 'Couldn’t do that just now. Try again.');
            }
        }).finally(() => {
            if (alive.current) {
                setBusy(false);
            }
        });
    };

    return (
        <PlanCardFace
            view={view}
            compact={compactDisplay}
            noPanel={windowKind() === 'other-popout'}
            busy={busy}
            note={note}
            onWatch={watch}
            onJoin={join}
        />
    );
};

export const PlanPostCard: React.FC<Props> = (props) => (
    <PlanCardBoundary post={props.post}>
        <PlanCard {...props}/>
    </PlanCardBoundary>
);
