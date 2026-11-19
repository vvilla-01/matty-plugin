import React from 'react';

import {clipThumbURL} from '../client';
import {
    CLIPPER_HINT,
    CLIPPER_LABEL,
    agoWords,
    clipCardLabel,
    clipErrorCopy,
    clipTime,
    expiryTag,
    streamLabel,
    tagLine,
    tagTitleOf,
    upvoteLabel,
} from '../clipLogic';
import {clipErrorCodeOf, clipErrorTextOf, shownTitleOf, voteOn} from '../clips';
import {nameNow} from '../name';
import type {Clip, ClipPerson, ClipTag} from '../types';

import {Avatar} from './Avatar';
import {ClipperBadge} from './badges';
import {CategoryIcon} from './categories';
import {Backdrop} from './LiveCard';
import {ThumbsUpIcon} from './icons';

// One clip in the Clips dialog's grid, and the pieces the dialog's viewer
// shares with it: the picture, the upvote button and a person's name with the
// Best Clipper's badge beside it.

// The Best Clipper's badge beside a clipper's name, as everywhere else it
// appears beside a name.
export const ClipperMark: React.FC<{size?: number}> = ({size = 14}) => (
    <span
        className='vntv-clipper-mark'
        role='img'
        aria-label={CLIPPER_LABEL}
        title={CLIPPER_HINT}
    ><ClipperBadge size={size}/></span>
);

// A face and a name, with the badge when they hold it. The face says nothing
// to a screen reader: the name is beside it.
export const ClipName: React.FC<{person: ClipPerson; best?: boolean; label?: string}> = ({person, best, label}) => (
    <span className='vntv-clip-person'>
        <Avatar
            userId={person.userId}
            name={person.name}
            className='vntv-clip-face'
            decorative={true}
        />
        <span className='vntv-clip-name'>{label || person.name}</span>
        {best ? <ClipperMark/> : null}
    </span>
);

// The clip's own picture, or, with none — or one that will not load, as when
// an admin has just taken it down — its streamer's picture blurred, as a Live
// now card's is before it has a still. Fills the box it is put in.
export const ClipPicture: React.FC<{clip: Clip}> = ({clip}) => {
    const url = clip.thumb ? clipThumbURL(clip.id, clip.thumb) : '';
    const [failed, setFailed] = React.useState(false);

    React.useEffect(() => setFailed(false), [url]);

    if (url === '' || failed) {
        return <Backdrop userId={clip.streamer.userId}/>;
    }

    return (
        <img
            className='vntv-clipcard-thumb'
            src={url}
            alt=''
            loading='lazy'
            draggable={false}
            onError={() => setFailed(true)}
        />
    );
};

// The upvote button (spec 3.10): a thumbs up and "14", pressed once the
// reader has voted.
// Its name stays the same whether or not it is pressed — aria-pressed says
// which — and changes only with the count. It shows the vote at once, and
// puts it back if the server says no (clips.ts voteOn), saying why in the
// line under it (under the card, in the grid) for a few seconds. On the
// reader's own clip it is a count, not a button.
const NOTE_MS = 8000;

export const UpvotePill: React.FC<{clip: Clip}> = ({clip}) => {
    const [note, setNote] = React.useState('');
    const alive = React.useRef(true);

    React.useEffect(() => () => {
        alive.current = false;
    }, []);

    React.useEffect(() => {
        if (note === '') {
            return undefined;
        }

        const timer = window.setTimeout(() => setNote(''), NOTE_MS);

        return () => window.clearTimeout(timer);
    }, [note]);

    if (clip.mine || !clip.canVote) {
        return (
            <span
                className='vntv-upvote vntv-upvote-static'
                title={clip.mine ? 'Your clip — upvotes from others count toward Best Clipper' : undefined}
            >
                <ThumbsUpIcon size={13}/>
                <span>{clip.upvotes}</span>
                <span className='vntv-sr-only'>{` ${clip.upvotes === 1 ? 'upvote' : 'upvotes'}`}</span>
            </span>
        );
    }

    return (
        <span className='vntv-upvote-wrap'>
            <button
                type='button'
                className='vntv-upvote'
                aria-pressed={clip.voted}
                aria-label={upvoteLabel(clip)}
                onClick={() => {
                    setNote('');
                    voteOn(clip.id, !clip.voted).catch((reason: unknown) => {
                        if (alive.current) {
                            setNote(clipErrorCopy(clipErrorCodeOf(reason), 'vote', {brand: nameNow()}, clipErrorTextOf(reason)).text);
                        }
                    });
                }}
            >
                <ThumbsUpIcon size={13}/>
                <span>{clip.upvotes}</span>
            </button>

            {/* Always there, so what is put in it is announced. */}
            <span
                className='vntv-upvote-note'
                role='status'
            >{note}</span>
        </span>
    );
};

// What the clip's stream was tagged with (addendum 4.14), under its title, as
// the Live now card shows what a stream is on: the kind's icon and the title.
// Words only, inside the card's own button — a button cannot hold another —
// and said to a screen reader as part of the card's name, so this says
// nothing itself. Nothing at all for a clip with no tag.
export const ClipTagChip: React.FC<{tag?: ClipTag}> = ({tag}) => {
    const title = tagTitleOf(tag);
    if (!tag || title === '') {
        return null;
    }

    return (
        <span
            className='vntv-onnow vntv-clipcard-tag'
            title={tagLine(tag)}
            aria-hidden={true}
        >
            <CategoryIcon
                category={tag.category}
                className='vntv-onnow-kind'
            />
            <span className='vntv-onnow-title'>{title}</span>
        </span>
    );
};

type CardProps = {
    clip: Clip;

    // Made since the reader last opened the list.
    fresh: boolean;

    // The clipper is the Best Clipper.
    best: boolean;
    now: number;
    onOpen: (id: string) => void;
};

// The card: one button that plays it, holding the picture, its tags, the
// title, what the stream was on, who made it, and from whose stream how long
// ago, on a line of its own so it is not cut short; and beside the maker's
// name — a button cannot hold another — the upvote. The play button is named in one sentence, so a screen reader hears
// the clip rather than the pieces of the card.
export const ClipCard: React.FC<CardProps> = ({clip, fresh, best, now, onOpen}) => {
    const shown = shownTitleOf(clip);
    const tag = expiryTag(clip.expiresAt, now);

    return (
        <li
            className='vntv-clipcard'
            data-new={fresh ? 'true' : undefined}
        >
            <button
                type='button'
                className='vntv-clipcard-play'
                data-clip={clip.id}
                aria-label={clipCardLabel(clip, shown, now, tagTitleOf(clip.tag))}
                onClick={() => onOpen(clip.id)}
            >
                <span className='vntv-clipcard-pic'>
                    <ClipPicture clip={clip}/>
                    <span className='vntv-clipcard-dur'>{clipTime(clip.durationMs)}</span>
                    {tag ? <span className='vntv-clipcard-expiry'>{tag}</span> : null}
                    {fresh ? <span className='vntv-clipcard-new'>{'New'}</span> : null}
                </span>
                <span
                    className='vntv-clipcard-title'
                    data-untitled={clip.title === '' ? 'true' : undefined}
                    title={shown}
                >{shown}</span>
                <ClipTagChip tag={clip.tag}/>
                <span className='vntv-clipcard-meta'>
                    <ClipName
                        person={clip.clipper}
                        best={best}
                    />
                </span>
                <span className='vntv-clipcard-where'>{`${streamLabel(clip.streamer)} · ${agoWords(clip.createdAt, now)}`}</span>
            </button>

            <UpvotePill clip={clip}/>
        </li>
    );
};
