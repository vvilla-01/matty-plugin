import React from 'react';

import {mediaImageURL} from '../client';
import {FAN_TITLE, fanOf, hoursInWords} from '../fans';
import {useSnapshot} from '../store';
import {formatWhen, viewerZone} from '../time';
import {voiceOf} from '../voice';
import {accessNow, useAccess} from '../identity';
import {mayWatch} from '../lockLogic';
import {popoverRowsFor} from '../accountsLogic';
import {useAccountsFor} from '../useAccounts';
import {useBoardsLookedAt} from '../useBoards';
import {contrastingSimpleColor} from '../color';
import {openLock} from '../lockPending';
import {markOpenedByHand} from '../watching';
import {watchOrOpen} from '../twitch';
import {channelURL, popoverStream, watchingOnTwitchWords} from '../twitchLogic';
import type {MediaInfo, Snapshot, Stream} from '../types';

import {formatUptime} from '../format';
import {artShape} from '../mediaRefs';

import {ScheduleIcon} from './icons';

import {HytaleRow, SteamRow} from './AccountRows';
import {AwardList, recordOf} from './AwardList';
import {Avatar} from './Avatar';
import {FanBadge, VoiceBadge} from './badges';
import {categoryOf, CategoryIcon} from './categories';
import {describeNowPlaying, nowPlayingLine} from './NowPlaying';

type Props = {
    user?: {id?: string};
};

// Watch, from somebody's profile: the stream added and the panel opened, as
// always — or, for a reader who may not watch, the lock, naming the stream,
// which starts once it is unlocked. Nothing is added to the watch list
// before then (design/lock-screen.md 7). Either way the person's own press
// opened the panel. A Twitch stream that cannot play in the panel opens on
// twitch.tv instead, and one whose tile would be too narrow opens the
// theatre with it (twitch.ts watchOrOpen).
function watchFromPopover(stream: Stream, name: string): void {
    if (!mayWatch(accessNow())) {
        openLock({kind: 'stream', handle: stream.handle, name});

        return;
    }

    markOpenedByHand();
    watchOrOpen(stream, 'add');
}

// Whether Mattermost's theme around an element is a dark one, decided as
// Mattermost decides which text goes on a colour (color.ts): from the
// centre channel's background, which the popover is drawn on. The Steam and
// Hytale pills need it for their words (the stylesheet's 3.4.0 popover
// section); every other colour in the popover follows the theme by itself.
function useDarkTheme(ref: React.RefObject<HTMLElement>): boolean {
    const [dark, setDark] = React.useState(false);

    React.useLayoutEffect(() => {
        const element = ref.current;
        if (!element) {
            return;
        }

        const background = themeBackground(getComputedStyle(element));
        const next = background !== '' && contrastingSimpleColor(background) === '#FFFFFF';
        if (next !== dark) {
            setDark(next);
        }
    });

    return dark;
}

// The centre channel's background as six hex digits: from the "r, g, b"
// Mattermost sets beside every theme colour (the popover's own stylesheet
// reads --center-channel-bg-rgb), else from the colour itself when that is
// already hex. '' when neither can be read, and then the theme counts as
// light, which is how the popover looked before.
function themeBackground(style: CSSStyleDeclaration): string {
    const channels = style.getPropertyValue('--center-channel-bg-rgb').split(',').map((part) => Number(part.trim()));
    if (channels.length === 3 && channels.every((value) => Number.isInteger(value) && value >= 0 && value <= 255)) {
        return channels.map((value) => value.toString(16).padStart(2, '0')).join('');
    }

    const hex = style.getPropertyValue('--center-channel-bg').trim();

    return /^#[0-9a-f]{6}$/i.test(hex) ? hex : '';
}

// Rendered inside the profile popover, under the standard user attributes.
//
// Five things, any of which is reason enough to appear: this person is live
// right now, they are on Mumble, they have connected Steam or Hytale, they
// hold an award (for airtime, or the Top Snooper's binoculars), or they have
// said when they are streaming next. Somebody with none of them gets nothing
// added to their profile at all.
//
// Steam and Hytale only for a reader who may watch (design/3.4.0.md 5.4,
// Victor's answer of 2026-11-11): the first rows here a locked reader does
// not get, outside the panel too, and for them the accounts are never even
// asked for.
//
// The planned streams are read from the snapshot the panel already receives —
// no extra request, and no per-user endpoint to build. The snapshot carries
// instants and nothing formatted, so the date is worked out here, in the
// reader's own zone, the same way the Up next rail does it.
export const PopoverLive: React.FC<Props> = ({user}) => {
    const snapshot = useSnapshot();
    const access = useAccess();
    const accounts = useAccountsFor(access);

    // The boards as this page holds them, which is from when it started or
    // from the last change to them, and asked about again as the popover
    // opens if that is a minute old: the figures on a record's rows tick.
    const boards = useBoardsLookedAt();
    const rootRef = React.useRef<HTMLDivElement | null>(null);
    const dark = useDarkTheme(rootRef);

    const userID = user?.id;
    if (!userID) {
        return null;
    }

    // Their Steam status and Hytale username, as GET /api/v1/accounts has
    // them: never for a reader who may not watch, whatever the store still
    // holds for a moment. popoverRowsFor is that rule, and these rows are
    // all the popover draws of either service (check-accounts.mjs).
    const accountRows = popoverRowsFor(accounts, userID, access);

    // One stream a person: their Broadcast Box stream on the air, else their
    // Twitch one, never a Twitch entry covered by their Broadcast Box card
    // (twitchLogic.ts popoverStream).
    const stream = popoverStream(snapshot.streams, userID);
    const live = stream && !stream.paused;
    const twitch = stream?.source === 'twitch';

    const planned = (snapshot.schedule || []).
        filter((item) => item.userId === userID).
        slice().
        sort((a, b) => (a.startsAt - b.startsAt) || (a.id < b.id ? -1 : 1));

    const voice = voiceOf(snapshot, userID);

    // Their biggest fan, when they have one (design/3.2.0.md 4.5), and their
    // record: both from the boards, which are not in the snapshot.
    const fan = fanOf(boards, userID);

    if (!live && !voice && accountRows.length === 0 && planned.length === 0 && !fan && recordOf(boards, userID).length === 0) {
        return null;
    }

    const zone = viewerZone();
    const now = Date.now();

    // What is on right now, as the server decided it for their card: what
    // they said with What's on?, or else the title of their plan that is on.
    // One rule, in one place (nowPlayingFor, in nowplaying.go), so this row
    // can never name something other than their card does.
    const onNow = live ? stream?.nowPlaying : undefined;
    const onNowMedia = onNow?.media;

    return (
        <div
            ref={rootRef}
            className='vntv vntv-inline vntv-popover'
            data-theme-tone={dark ? 'dark' : undefined}
        >
            {/* First, above the medals: what somebody is doing right now
                outranks what they did in March.

                Built like an award row, because it is the same kind of thing:
                one fact about this person, with the thing it is about on the
                left and what it says on the right. A bare pill reading "● Live"
                next to a bare pill reading "Watch" gave equal weight to a
                statement and an action, and told you nothing about WHAT was on. */}
            {live && stream ? (
                <div
                    className='vntv-factrow vntv-factrow-live'
                    data-media={onNowMedia ? 'true' : undefined}
                    title={onNow ? describeNowPlaying(onNow) : undefined}
                >
                    {onNowMedia ? (
                        <RowArt
                            key={onNowMedia.ref}
                            media={onNowMedia}
                        />
                    ) : null}
                    {onNowMedia ? (
                        <span
                            className='vntv-factrow-mark vntv-factrow-cover'
                            data-kind={onNowMedia.kind}
                            data-art={artShape(onNowMedia.ref)}
                            aria-hidden={true}
                        >
                            <Cover
                                key={onNowMedia.ref}
                                media={onNowMedia}
                            />
                            <span className='vntv-factrow-dot vntv-factrow-dot-on'/>
                        </span>
                    ) : null}

                    {/* Said with no title to look up: its kind where the
                        cover would be, with the live dot on its corner. */}
                    {onNow && !onNowMedia && categoryOf(onNow.category) ? (
                        <span
                            className='vntv-factrow-mark vntv-factrow-onnow'
                            aria-hidden={true}
                        >
                            <CategoryIcon
                                category={onNow.category}
                                className='vntv-factrow-kind'
                                size={18}
                            />
                            <span className='vntv-factrow-dot vntv-factrow-dot-on'/>
                        </span>
                    ) : null}
                    {!onNowMedia && !(onNow && categoryOf(onNow.category)) ? (
                        <span
                            className='vntv-factrow-mark'
                            aria-hidden={true}
                        ><span className='vntv-factrow-dot'/></span>
                    ) : null}
                    <span className='vntv-factrow-text'>
                        <span className='vntv-factrow-title'>{'Live now'}</span>
                        {/* The whole line in its tooltip: a Twitch stream's is
                            long, and the popover is narrow. */}
                        <span
                            className='vntv-factrow-meta'
                            title={twitch ? metaOf(stream, onNow ? nowPlayingLine(onNow) : '') : undefined}
                        >{metaOf(stream, onNow ? nowPlayingLine(onNow) : '')}</span>
                        {stream.alsoTwitch && channelURL(stream.alsoTwitch) !== '' ? (
                            <a
                                className='vntv-twitch-out'
                                href={channelURL(stream.alsoTwitch)}
                                target='_blank'
                                rel='noopener noreferrer'
                            >{'Also on Twitch ↗'}</a>
                        ) : null}
                    </span>
                    <button
                        type='button'
                        className='vntv-pill vntv-factrow-action'
                        title={twitch ? 'Watch on Twitch, here or on twitch.tv' : undefined}
                        onClick={() => watchFromPopover(stream, stream.displayName || stream.username || stream.title)}
                    >{'Watch'}</button>
                </div>
            ) : null}

            {/* Second, and above the medals for the same reason as the live
                row: it is about now. The same headphones as beside their name
                in a post, so the two read as the same fact. */}
            {voice ? (
                <div className='vntv-factrow vntv-factrow-voice'>
                    <span
                        className='vntv-factrow-mark'
                        aria-hidden={true}
                    ><VoiceBadge
                        muted={voice.muted}
                        size={22}
                    /></span>
                    <span className='vntv-factrow-text'>
                        <span className='vntv-factrow-title'>{'On Mumble'}</span>
                        <span className='vntv-factrow-meta'>{voice.muted ? 'In voice · muted' : 'In voice now'}</span>
                    </span>
                </div>
            ) : null}

            {/* Third and fourth: who they are on Steam and Hytale, and on
                Steam what they are doing. After Mumble, which is the server's
                own room; before the medals, which are about the past. */}
            {accountRows.map((row) => (row.service === 'steam' ? (
                <SteamRow
                    key={row.service}
                    row={row}
                />
            ) : (
                <HytaleRow
                    key={row.service}
                    row={row}
                />
            )))}

            <AwardList userId={userID}/>

            {/* Who has watched them most, after their own awards: it is a
                fact about them, but somebody else's doing. The fan's face
                where a badge goes, with the heart on its corner, so it reads
                as the same title the fan wears by their own name. */}
            {fan ? (
                <div
                    className='vntv-factrow vntv-factrow-fan'
                    data-rank={1}
                >
                    <span
                        className='vntv-factrow-mark vntv-factrow-fanface'
                        aria-hidden={true}
                    >
                        <Avatar
                            userId={fan.fanId}
                            name={fan.fan}
                            decorative={true}
                        />
                        <span className='vntv-factrow-fanpip'><FanBadge size={12}/></span>
                    </span>
                    <span className='vntv-factrow-text'>
                        <span className='vntv-factrow-title'>{FAN_TITLE}</span>
                        <span className='vntv-factrow-meta'>{`${fan.fan} · ${hoursInWords(fan.hours)}`}</span>
                    </span>
                </div>
            ) : null}

            {planned.length > 0 && (
                <>
                    <p className='vntv-upnext-eyebrow'>{'Streaming next'}</p>
                    <ul className='vntv-popover-plans'>
                        {planned.map((item) => (item.media ? (

                            // Linked to a movie, show or game: its cover where
                            // the category icon goes, its art faded in behind,
                            // and which title it is on a line of its own. No
                            // link out: the popover is a glance at a person,
                            // and the Up next profile is where the title has
                            // its IMDb, Steam or IGDB page.
                            <li
                                key={item.id}
                                className='vntv-factrow'
                                data-media='true'
                                title={`${item.title} — ${formatWhen(item.startsAt, zone, now)}\n${describe(item.media)}`}
                            >
                                <RowArt
                                    key={item.media.ref}
                                    media={item.media}
                                />
                                <span
                                    className='vntv-factrow-mark vntv-factrow-cover'
                                    data-kind={item.media.kind}
                                    data-art={artShape(item.media.ref)}
                                    aria-hidden={true}
                                >
                                    <Cover
                                        key={item.media.ref}
                                        media={item.media}
                                    />
                                </span>

                                <span className='vntv-factrow-text'>
                                    <span className='vntv-factrow-title'>{formatWhen(item.startsAt, zone, now)}</span>
                                    <span className='vntv-factrow-meta'>{item.title}</span>
                                    <span className='vntv-factrow-media'>{titleLine(item.media)}</span>
                                </span>
                            </li>
                        ) : (
                            <li
                                key={item.id}
                                className='vntv-factrow'
                                title={`${categoryOf(item.category)?.label ?? 'Streaming'}: ${item.title} — ${formatWhen(item.startsAt, zone, now)}`}
                            >
                                {/* The category mark when they said what it is,
                                    and a calendar when they did not — so the
                                    headline lines up with the medals above it
                                    either way. */}
                                <span
                                    className='vntv-factrow-mark'
                                    aria-hidden={true}
                                >
                                    {categoryOf(item.category) ? (
                                        <CategoryIcon
                                            category={item.category}
                                            className='vntv-factrow-kind'
                                            size={15}
                                        />
                                    ) : <ScheduleIcon/>}
                                </span>

                                <span className='vntv-factrow-text'>
                                    <span className='vntv-factrow-title'>{formatWhen(item.startsAt, zone, now)}</span>
                                    <span className='vntv-factrow-meta'>{item.title}</span>
                                </span>
                            </li>
                        )))}
                    </ul>
                </>
            )}
        </div>
    );
};

// The second line, for either kind of stream: a Twitch stream says it is on
// Twitch, and that its count is Twitch's whole audience.
function metaOf(stream: Stream, onNow: string): string {
    if (stream.source !== 'twitch') {
        return liveMeta(onNow ? {...stream, title: onNow} : stream);
    }

    return [onNow, 'Live on Twitch', formatUptime(stream.streamStart), watchingOnTwitchWords(stream.viewers)].filter(Boolean).join(' · ');
}

// The second line of the live card: what is on, and how long it has been on.
// Either alone is thin; the title without the uptime reads as a label rather
// than as something happening.
function liveMeta(stream: {title: string; streamStart?: string; viewers: number}): string {
    const parts = [stream.title];

    const uptime = formatUptime(stream.streamStart);
    if (uptime) {
        parts.push(uptime);
    }

    if (stream.viewers > 0) {
        parts.push(`${stream.viewers} watching`);
    }

    return parts.join(' · ');
}

// "Heat · 1995 · Movie": which title a plan is linked to, in one line.
function titleLine(media: MediaInfo): string {
    const kind = media.kind === 'tv' ? 'TV series' : (media.kind === 'game' ? 'Game' : 'Movie');

    return [media.title, media.year, kind].filter(Boolean).join(' · ');
}

// The rest of what is known about it, for the tooltip: the popover has room
// for a line about a title, not a paragraph.
function describe(media: MediaInfo): string {
    return [titleLine(media), media.facts, media.overview].filter(Boolean).join('\n');
}

// The cover — poster, or box art — relayed by the server like all of it. The
// category icon stands in if it will not load, or if lookups have since been
// switched off, when the server stops relaying art.
const Cover: React.FC<{media: MediaInfo}> = ({media}) => {
    const [failed, setFailed] = React.useState(false);

    if (failed) {
        return (
            <CategoryIcon
                category={media.kind === 'game' ? 'game' : 'movie'}
                className='vntv-factrow-kind'
                size={15}
            />
        );
    }

    return (
        <img
            src={mediaImageURL(media.ref, 'thumb')}
            alt=''
            loading='lazy'
            draggable={false}
            onError={() => setFailed(true)}
        />
    );
};

// The wide art, faded in behind the row: the same smoked glass as its card in
// Up next, lighter, since this sits in Mattermost's own popover on whatever
// theme the reader has.
const RowArt: React.FC<{media: MediaInfo}> = ({media}) => {
    const [failed, setFailed] = React.useState(false);
    if (failed) {
        return null;
    }

    return (
        <img
            className='vntv-factrow-art'
            src={mediaImageURL(media.ref, 'backdrop')}
            alt=''
            aria-hidden={true}
            loading='lazy'
            draggable={false}
            onError={() => setFailed(true)}
        />
    );
};
