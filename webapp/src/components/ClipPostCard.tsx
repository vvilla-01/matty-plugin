import React from 'react';

import {clapperBeside} from '../awardLogic';
import {clipThumbURL} from '../client';
import {windowKind} from '../away';
import {CARD_UNCHECKED, cardView, cardVoteWords} from '../clipLogic';
import type {CardText, CardView} from '../clipLogic';
import {askForMeOnce, clipErrorCodeOf, clipErrorTextOf, refreshSharedClip, untitledName, useSharedClip, voteOnCard, watchSharedClip} from '../clips';
import {accessNow, refreshIdentity, useAccess, useIdentity} from '../identity';
import {mayWatch} from '../lockLogic';
import {nameNow, useName} from '../name';
import {useSnapshot} from '../store';

import {CategoryIcon} from './categories';
import {ClipperMark} from './ClipCard';
import {ClapperIcon, PlayIcon, ThumbsUpIcon} from './icons';
import {Backdrop} from './LiveCard';

// A shared clip, as a post in a channel shows it (addendum 4.9): the card
// Mattermost draws in place of the message of every post of the plugin's clip
// type, wherever the post appears — the channel, threads, search, pinned and
// saved messages, permalink previews. Registered in index.tsx in every kind of
// window.
//
// Who reads it decides what it shows, and the rules are clipLogic.ts's
// cardView:
//
//   - A reader who may watch clips — linked, or let in by an admin — sees
//     what the server says about the clip now (one batched request for every
//     card in the channel, useSharedClip), its picture, and Watch, which
//     opens it in the panel.
//   - A locked reader sees only what the post itself carries — the title, who
//     clipped it from whose stream, its length and the stream's tag — with
//     no picture and no request at all, and Watch and the picture open
//     the panel's lock, the clip waiting to open once it is unlocked
//     (design/lock-screen.md 7). In a window with no panel (a popped-out
//     thread), there is nothing to open: the card says where to unlock it.
//   - Expired and deleted clips say so, for everybody, from the post alone.
//
// The post's props are written by the plugin's server, but anybody can make a
// post of this type while the plugin is not running, and an author can try to
// edit one, so they are only ever shown as text, and a linked reader sees
// the server's word instead.
//
// Colours are Mattermost's theme's, not the panel's: the card sits on the
// channel, which is white for most people (styles.ts, "Clips: a shared clip
// in a channel").
//
// Clicks. Inside a permalink preview, Mattermost follows the permalink on a
// click whose target is not an A, IMG, BUTTON or I element, or something with
// role button: a click on an svg follows it too (post_attachment_container.tsx,
// 11.11). So every control is a real button, nothing inside a button and no
// svg takes the pointer (styles.ts), and the eyebrow's icon sits in an <i>: a
// click on any of them lands on something Mattermost leaves alone.

// What Mattermost hands a post type's component. post is Mattermost's post,
// whose props are whatever the post carries.
type Props = {
    post: {id?: string; type?: string; message?: string; props?: unknown};
    compactDisplay?: boolean;
    isRHS?: boolean;
    theme?: unknown;
};

// The post's message, or '' for one that is not a string or cannot be read:
// what the card falls back to when it cannot draw.
function messageOf(post: unknown): string {
    try {
        const message = (post as {message?: unknown} | null)?.message;

        return typeof message === 'string' ? message : '';
    } catch {
        return '';
    }
}

// Mattermost 11.11 wraps a post type's component in its own error boundary,
// which puts its plugin error notice where the card was
// (post_message_view.tsx, PluggableErrorBoundary). This one catches first: a
// card that fails shows the post's own message instead, as plain text — the plugin's
// fixed words and the link, on a genuine post — and tries again when the post
// changes (an edit Mattermost pushes). A copy of MiniPlayer's Quiet, with a
// fallback.
class ClipCardBoundary extends React.Component<{post: unknown; children: React.ReactNode}, {failed: boolean}> {
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

// The title as the card shows it: the clip's own, or else its streamer and
// when it was made, "Dave, 3 Oct 21:04", in the reader's zone. With no
// streamer's name either, the server's name for the plugin: "Matty clip".
function shownTitle(text: CardText, brand: string): string {
    if (text.title !== '') {
        return text.title;
    }

    const name = text.streamer.name.trim() === '' ? `${brand} clip` : text.streamer.name;

    return untitledName({...text.streamer, name}, text.createdAt);
}

// The picture: the clip's own, the streamer's blurred where it has none (or
// it will not load), or the placeholder clapper. Only ever more than the
// placeholder for a reader who may watch, with the server's card in hand.
const Picture: React.FC<{view: CardView}> = ({view}) => {
    const url = view.picture === 'thumb' && view.thumb ? clipThumbURL(view.id, view.thumb) : '';
    const [failed, setFailed] = React.useState(false);

    React.useEffect(() => setFailed(false), [url]);

    if (url !== '' && !failed) {
        return (
            <img
                className='vntv-postclip-thumb'
                src={url}
                alt=''
                loading='lazy'
                draggable={false}
                onError={() => setFailed(true)}
            />
        );
    }

    const streamer = view.text?.streamer.userId;
    if ((view.picture === 'thumb' || view.picture === 'backdrop') && streamer) {
        return <Backdrop userId={streamer}/>;
    }

    return (
        <span className='vntv-postclip-placeholder'>
            <ClapperIcon size={26}/>
        </span>
    );
};

// The thumbs up on the card (design/3.2.0.md 9.3, Victor's P6 pick): a pill
// after Watch, the panel's upvote in the theme's colours. A button for a linked
// reader who may vote, pressed once they have; for a reader who may not (let
// in, or on their own clip) the same pill as a plain count, dashed, its hand
// filled where a let-in reader's old vote is still shown (9.2). Nothing for a
// locked reader, whose card has no count to show. A real button whose svg
// takes no pointer, because a press elsewhere inside a permalink preview
// follows the permalink. A vote that did not count says why under the card:
// the clip vote's own words for a refusal, "Couldn't count that. Try again."
// for anything else (cardVoteWords).

const CardThumb: React.FC<{
    id: string;
    votes: {count: number; voted: boolean; canVote: boolean};
    own: boolean;
    onFailed: (text: string) => void;
}> = ({id, votes, own, onFailed}) => {
    if (!votes.canVote) {
        return (
            <span
                className='vntv-postclip-up vntv-postclip-up-static'
                role='img'
                aria-label={`${votes.count} thumbs up`}
                title={own ? 'Thumbs up on your clip' : undefined}
                data-voted={votes.voted ? 'true' : undefined}
            >
                <ThumbsUpIcon size={13}/>
                <span>{votes.count}</span>
            </span>
        );
    }

    return (
        <button
            type='button'
            className='vntv-postclip-up'
            aria-pressed={votes.voted}
            aria-label={`Thumbs up, ${votes.count}`}
            title={votes.voted ? 'You gave this a thumbs up. Click to take it back.' : 'Give this clip a thumbs up'}
            onClick={() => {
                onFailed('');
                voteOnCard(id, !votes.voted).catch((reason: unknown) => onFailed(cardVoteWords(clipErrorCodeOf(reason), clipErrorTextOf(reason), nameNow())));
            }}
        >
            <ThumbsUpIcon size={13}/>
            <span>{votes.count}</span>
        </button>
    );
};

const PostCard: React.FC<Props> = ({post, compactDisplay}) => {
    const [voteNote, setVoteNote] = React.useState('');
    const me = useIdentity();
    const access = useAccess();
    const brand = useName();
    let watch: boolean | undefined;
    if (mayWatch(access)) {
        watch = true;
    } else if (access === 'locked') {
        watch = false;
    }
    const snapshot = useSnapshot();
    const retentionDays = snapshot.clips?.retentionDays;
    const now = Date.now();

    // What the post says before anything is asked, to know what to ask for:
    // only the card of a reader who may watch asks, and never about a clip
    // the post already says has gone.
    const before = cardView({post, mayWatch: watch, retentionDays, now, fetch: {status: 'idle'}, name: brand});
    const asks = watch === true && before.state === 'loading';
    const fetch = useSharedClip(asks ? before.id : '', watch);
    const view = asks ? cardView({post, mayWatch: watch, retentionDays, now, fetch, name: brand}) : before;

    // Somebody signed in since the page loaded, with no "me" yet: asked for
    // once, by whichever card mounts first, so the cards need not wait for
    // the panel to be opened (addendum 2.2).
    React.useEffect(() => {
        if (me === null) {
            askForMeOnce();
        }
    }, [me]);

    // Watch, for a reader who was not known: checking while "me" is asked for
    // again; note when it still could not be.
    const [checking, setChecking] = React.useState(false);
    const [note, setNote] = React.useState('');
    const alive = React.useRef(true);

    React.useEffect(() => () => {
        alive.current = false;
    }, []);

    // The note answering an unknown reader's press goes once they are known.
    React.useEffect(() => {
        if (view.state !== 'unknown') {
            setNote('');
        }
    }, [view.state]);

    // A vote that did not count says so under the card for 8 s (9.3).
    React.useEffect(() => {
        if (voteNote === '') {
            return undefined;
        }

        const timer = window.setTimeout(() => setVoteNote(''), 8000);

        return () => window.clearTimeout(timer);
    }, [voteNote]);

    // A popped-out thread, or any window but the main one and a popped-out
    // channel, has no side panel to show the lock in.
    const nowhereToUnlock = windowKind() === 'other-popout';

    const watchClip = () => {
        if (!view.watch || checking) {
            return;
        }

        if (view.state === 'unknown') {
            setChecking(true);
            setNote('');
            refreshIdentity().then((fresh) => {
                if (!alive.current) {
                    return;
                }

                setChecking(false);
                if (!fresh) {
                    setNote(CARD_UNCHECKED);

                    return;
                }
                if (mayWatch(accessNow())) {
                    refreshSharedClip(view.id);
                }

                // Opens the clip, or for a locked reader the lock with the
                // clip waiting behind it (clips.ts).
                watchSharedClip(view.id);
            });

            return;
        }

        if (view.state !== 'locked') {
            refreshSharedClip(view.id);
        }
        watchSharedClip(view.id);
    };

    if (view.state === 'fallback') {
        return <p className='vntv-postclip-fallback'>{view.message}</p>;
    }

    const text = view.text;
    const title = text ? shownTitle(text, brand) : '';
    // The clapper beside "Clipped by" only while the Best Clipper's choice of
    // what shows by their name has it (design/3.2.0.md 3.5).
    const best = Boolean(text?.clipper.userId) && clapperBeside(snapshot) === text?.clipper.userId;

    let actions: React.ReactNode = null;
    const unlockElsewhere = view.state === 'locked' && nowhereToUnlock;
    if (unlockElsewhere) {
        actions = <p className='vntv-postclip-note'>{`Open ${brand} in the main window to unlock it.`}</p>;
    } else if (view.watch) {
        actions = (
            <div className='vntv-postclip-actions'>
                <button
                    type='button'
                    className='vntv-postclip-watch'
                    aria-label={view.watchLabel}
                    aria-disabled={checking ? true : undefined}
                    onClick={watchClip}
                >
                    <PlayIcon size={12}/>
                    {'Watch'}
                </button>
                {view.votes ? (
                    <CardThumb
                        id={view.id}
                        votes={view.votes}
                        own={Boolean(me?.userId && text?.clipper.userId === me.userId)}
                        onFailed={setVoteNote}
                    />
                ) : null}
            </div>
        );
    }

    return (
        <div
            className='vntv vntv-postclip'
            role='group'
            aria-label={view.label}
            data-state={view.state}
            data-dimmed={view.dimmed ? 'true' : undefined}
            data-compact={compactDisplay ? 'true' : undefined}
        >
            {/* The picture does what Watch does, for the mouse; to the
                keyboard and a screen reader Watch is the one way. A button
                even where it does nothing, so a click on it inside a
                permalink preview is left alone. */}
            <button
                type='button'
                className='vntv-postclip-pic'
                tabIndex={-1}
                aria-hidden={true}
                onClick={view.watch && !unlockElsewhere ? watchClip : undefined}
            >
                <Picture view={view}/>
                {text ? <span className='vntv-postclip-dur'>{text.length}</span> : null}
            </button>

            <div className='vntv-postclip-body'>
                {text ? (
                    <>
                        <p className='vntv-postclip-eyebrow'>
                            <i
                                className='vntv-postclip-icon'
                                aria-hidden={true}
                            ><ClapperIcon size={12}/></i>
                            {`${brand} clip`}
                        </p>
                        <p className='vntv-postclip-title'>
                            <span
                                className='vntv-postclip-name'
                                title={title}
                            >{title}</span>
                            {text.tag ? (
                                <span
                                    className='vntv-postclip-tag'
                                    title={text.tag.word === '' ? text.tag.title : `${text.tag.title} · ${text.tag.word}`}
                                >
                                    <CategoryIcon
                                        category={text.tag.category}
                                        className='vntv-postclip-tag-kind'
                                        size={12}
                                    />
                                    <span className='vntv-postclip-tag-title'>{text.tag.title}</span>
                                </span>
                            ) : null}
                        </p>
                        <p className='vntv-postclip-meta'>
                            {text.clipper.name.trim() !== '' ? (
                                <span>
                                    {`Clipped by ${text.clipper.name}`}
                                    {best ? <ClipperMark size={16}/> : null}
                                </span>
                            ) : null}
                            {text.stream !== '' ? <span>{text.stream}</span> : null}
                            <span>{text.length}</span>
                        </p>
                    </>
                ) : null}

                {view.sentence ? <p className='vntv-postclip-gone'>{view.sentence}</p> : null}

                {/* A check that failed, said by the card itself: plain words,
                    so a channel of cards that could not be checked is not
                    read out card by card. */}
                {view.note ? <p className='vntv-postclip-note'>{view.note}</p> : null}

                {/* The answer to a press of Watch by a reader who could not
                    be checked: always there, so what is put in it is
                    announced while focus stays on Watch. */}
                <p
                    className='vntv-postclip-note vntv-postclip-answer'
                    role='status'
                >{(note !== '' && !view.note ? note : '') || voteNote}</p>
                {actions}
            </div>
        </div>
    );
};

export const ClipPostCard: React.FC<Props> = (props) => (
    <ClipCardBoundary post={props.post}>
        <PostCard {...props}/>
    </ClipCardBoundary>
);
