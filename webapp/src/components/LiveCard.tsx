import React from 'react';

import {stillURL, streamPictureURL, streamThumbnailURL, twitchPreviewURL} from '../client';
import {FAN_TITLE, fanOf, hoursInWords} from '../fans';
import {formatUptime} from '../format';
import {linkParts, renderPart} from '../linkify';
import type {LinkPart} from '../linkify';
import {channelURL, watchingOnTwitchWords} from '../twitchLogic';
import type {Stream, VoicePerson} from '../types';
import {useBoards} from '../useBoards';
import {voiceLabel} from '../voice';

import {AwardBeside} from './AwardMark';
import {Avatar} from './Avatar';
import {VoiceBadge} from './badges';
import {EyeIcon, MinusIcon, PlusIcon} from './icons';
import {nowPlayingLine, OnNow} from './NowPlaying';

type Props = {
    stream: Stream;

    // The streamer on Mumble, when they are: headphones in front of the medals.
    voice?: VoicePerson | null;

    watching: boolean;
    full: boolean;

    // Whether anything is playing in the panel at all. "Watch this as well"
    // only means something once something is: before that, the card itself
    // is how a stream is started, and the round button is not shown.
    anyWatching: boolean;

    // Whether more than one stream is playing. "Stop watching this" is only
    // on a card while it is: with a single stream playing, the power switch
    // on its picture is how it is turned off, and a second way on the card
    // was one too many.
    manyWatching: boolean;

    // Whether live streams have preview stills at all (a setting).
    previews: boolean;

    onWatchOnly: (handle: string) => void;
    onToggle: (handle: string) => void;
};

// The badges on a card are smaller than on the old row: they sit on a picture
// now, beside a name, rather than in a column of their own.
const BADGE_SIZE = 15;

// How many watchers' faces a card shows before the rest become "+N", as the
// Up next cards do for who has joined.
const WATCHER_FACES = 3;

// "Dave", "Dave and Bob", "Dave, Bob and Erin", "Dave, Bob and 2 more".
function namesOf(names: string[], more: number): string {
    const all = more > 0 ? [...names, `${more} more`] : names;
    if (all.length <= 1) {
        return all.join('');
    }

    return `${all.slice(0, -1).join(', ')} and ${all[all.length - 1]}`;
}

// The words of a title for the line that says what it is, with its links
// taken out — on their own an address says nothing — and the hashtags it ends
// on, which are tags on the end of what it says rather than part of it, and
// are on the line below. A hashtag in the middle stays, as the word it is:
// "Playing #arma3 with Dave" without it is not the same sentence. A dash or a
// colon left hanging where the cut was goes with them.
//
// An @name always stays, even at the end (an approved departure from
// design/3.2.0.md 8.3): it is a word of the sentence ("Co-op night with
// @rupert" is not the same without it), and the links line shows it as a
// chip only once Mattermost knows the person, so a name nobody has, or one
// still being looked up, would otherwise be shown nowhere.
function wordsOf(parts: LinkPart[]): string {
    let end = parts.length;
    while (end > 0 && !parts[end - 1].mention && (parts[end - 1].href || parts[end - 1].hashtag || !/[\p{L}\p{N}]/u.test(parts[end - 1].text))) {
        end -= 1;
    }

    const words = parts.slice(0, end).filter((part) => !part.href).map((part) => part.text).join('').replace(/\s+/g, ' ').trim();
    const cut = parts.slice(end).some((part) => part.href || part.hashtag);

    return cut ? words.replace(/[\s:;,|/·•–—-]+$/u, '') : words;
}

// How long a new still takes to fade in over the old one. Must agree with the
// transition on .vntv-live-layer in the stylesheet.
const CROSSFADE_MS = 420;

// A live stream, as a card the size of an Up next card: what is on it now, as
// a still, with whose it is and what they call it along the bottom.
//
// The picture is, in order of preference:
//   - a person's stream: the latest shared still (see stills.ts), over their
//     own picture blurred, which is what shows until the first still arrives —
//     or always, when previews are switched off;
//   - a 24/7 channel: the wide card picture an admin chose, or else its square
//     picture set like an album cover on a blur of itself.
//
// A Twitch stream's card (design/twitch-live.md 5.8) is the same card: its
// picture is the server's relay of Twitch's own preview, or the person's
// picture; its tags say Twitch, in the panel's own colours, with Twitch's
// whole audience; what is on is Twitch's category when nothing else says;
// and it always carries Watch on Twitch, the way back to the source that
// Twitch's agreement asks for. No faces, no binoculars, never paused. A
// Broadcast Box card whose streamer is also live on Twitch says so instead
// of a second card.
//
// Two controls, as before, and for the same reasons: the card means "watch
// only this" and the round button means "watch this as well". A button cannot
// hold another button, and the stream's title can hold links and hashtags,
// which cannot sit inside a button either — so the card's own button lies
// behind everything on it, and the round one is its sibling, laid over the
// corner.
export const LiveCard: React.FC<Props> = ({stream, voice, watching, full, anyWatching, manyWatching, previews, onWatchOnly, onToggle}) => {
    const boards = useBoards();

    // The round button: "watch this as well" on a card that is not playing,
    // once something is; "stop watching this" on one that is, while it is one
    // of several.
    const hasButton = watching ? manyWatching : anyWatching;

    // The round button can go while it has focus — stopping the second-last
    // stream elsewhere takes the minus off the one left playing — and focus
    // then goes to the card rather than falling out of the panel.
    const hitRef = React.useRef<HTMLButtonElement | null>(null);
    const addFocused = React.useRef(false);

    React.useLayoutEffect(() => {
        if (hasButton || !addFocused.current) {
            return;
        }

        addFocused.current = false;
        if (!document.activeElement || document.activeElement === document.body) {
            hitRef.current?.focus({preventScroll: true});
        }
    }, [hasButton]);

    const uptime = stream.alwaysOn ? '' : formatUptime(stream.streamStart);

    // Cut at 200 characters as the server counts them (poller.go), not at
    // 200 UTF-16 units: a title it allows that has emoji in it would otherwise
    // lose its last few letters here, and a hashtag at the end would become a
    // button searching for a shorter, different one.
    const note = Array.from(stream.motd || '').slice(0, 200).join('');
    const who = stream.displayName || (stream.username ? `@${stream.username}` : stream.title);

    // The title's words on one line, and what can be pressed in it — its
    // links, hashtags and @names — on a line of their own under them, rather
    // than the two run together: links and hashtags are usually at the end
    // of a title, and on a card this size the end is exactly what an ellipsis
    // cuts off, leaving one nobody can see or press and a tab stop on it. An
    // @name in the middle of a title stays in the words line as text and gets
    // its own control on the links line, as a hashtag does.
    //
    // Never an @name on a Twitch card: its titles use @ for Twitch handles,
    // and a Mattermost account by the same name would be the wrong person.
    const twitch = stream.source === 'twitch';
    const parts = note ? linkParts(note, !twitch) : [];
    const controls = parts.filter((part) => part.href || part.hashtag || part.mention);
    const words = wordsOf(parts);
    const picture = streamPictureURL(stream);

    // The badges beside the name come with the card, decided by the server
    // (awards_decide.go onCard): the person's own choice when they made one
    // (design/3.2.0.md 3.5), with this card's own places standing in for the
    // boards'. Everybody else and a card with no person keep 3.1.0's crown,
    // calendar and binoculars; a 24/7 card has none. A Twitch card follows
    // its person's choice like any other.
    const badges = stream.awards || [];

    // The streamer's biggest fan (Victor's P3 pick, the quiet line): never on
    // a Twitch card (no sessions the panel can see) or a 24/7 channel (nobody
    // to be a fan of). From the boards, which the panel reads as it opens.
    const fan = !twitch && !stream.alwaysOn ? fanOf(boards, stream.userId) : null;
    const still = (() => {
        if (stream.alwaysOn) {
            return streamThumbnailURL(stream) || '';
        }
        if (twitch) {
            return previews && stream.previewVersion ? twitchPreviewURL(stream.handle, stream.previewVersion) : '';
        }

        return previews && stream.stillVersion ? stillURL(stream.handle, stream.stillVersion) : '';
    })();

    // Where the card goes on twitch.tv: always, on a Twitch card; on a
    // Broadcast Box card, when its streamer is also live there.
    const twitchOut = channelURL((twitch ? stream.twitchLogin : stream.alsoTwitch) || '');

    // Who is watching: faces for the first few, and a count for the rest —
    // the rest including anybody watching some other way, who has no face.
    // Never on a Twitch card: nobody's watching there is seen by this server.
    const watchers = twitch ? [] : (stream.watchers || []);
    const faces = watchers.slice(0, WATCHER_FACES);
    // Faces not shown, plus the people the server could not name — sent as
    // "others" (and left out when there are none), since only it can tell a
    // second tab of somebody already shown from somebody else. Never the raw
    // count of connections, which counts that second tab as another person.
    const unseen = (watchers.length - faces.length) + (stream.others || 0);
    const watchedBy = watchers.length > 0 ? namesOf(faces.map((person) => person.who), unseen) : '';

    // What is on it, decided by the server: see NowPlaying.tsx. On a Twitch
    // card with nothing said, Twitch's category.
    const onNow = stream.alwaysOn ? undefined : stream.nowPlaying;
    const category = twitch && !onNow ? stream.twitchCategory || '' : '';

    const describe = [
        twitch ? 'on Twitch' : '',
        onNow ? `playing ${nowPlayingLine(onNow)}` : '',
        category ? `in ${category}` : '',
        stream.paused ? 'paused' : '',
        uptime ? `live for ${uptime}` : '',
        (() => {
            if (twitch) {
                return watchingOnTwitchWords(stream.viewers);
            }
            if (watchedBy) {
                return `watched by ${watchedBy}`;
            }

            return stream.viewers > 0 ? `${stream.viewers} watching` : '';
        })(),
        stream.alsoTwitch ? 'also live on Twitch' : '',
        fan ? `biggest fan ${fan.fan}` : '',
    ].filter(Boolean).join(', ');

    // What can be pressed in the title, on a line of its own: Watch on
    // Twitch, and the title's links, hashtags and @names.
    const linksLine = controls.length > 0 || twitchOut !== '' ? (
        <span className='vntv-live-links'>
            {twitchOut !== '' ? (
                <a
                    className='vntv-twitch-out'
                    href={twitchOut}
                    target='_blank'
                    rel='noopener noreferrer'
                    title={twitch ? `Watch ${stream.title} on twitch.tv` : `${stream.title} is also live on Twitch`}

                    // Its own press: the card behind it
                    // starts the stream in the panel.
                    onClick={(event) => event.stopPropagation()}
                    onKeyDown={(event) => {
                        if (event.key === 'Enter' && !event.ctrlKey && !event.metaKey && !event.altKey) {
                            event.stopPropagation();
                        }
                    }}
                >{twitch ? 'Watch on Twitch ↗' : 'Also on Twitch ↗'}</a>
            ) : null}
            {controls.map((part, index) => renderPart(part, index))}
        </span>
    ) : null;

    const addLabel = (() => {
        if (watching) {
            return `Stop watching ${stream.title}`;
        }
        if (full) {
            return 'Watching four streams already — close one first';
        }

        return `Also watch ${stream.title}`;
    })();

    return (
        <li
            className='vntv-live'
            data-source={twitch ? 'twitch' : undefined}
            data-watching={watching ? 'true' : undefined}
            data-paused={stream.paused ? 'true' : undefined}
            data-always={stream.alwaysOn ? 'true' : undefined}
            data-add={hasButton ? 'true' : undefined}
            data-fan={fan ? 'true' : undefined}
        >
            <div
                className='vntv-live-card'
                onClick={() => onWatchOnly(stream.handle)}
            >
                <button
                    ref={hitRef}
                    type='button'
                    className='vntv-live-hit'
                    aria-pressed={watching}
                    aria-label={`Watch ${stream.title}${note ? `: ${note}` : ''}${describe ? ` (${describe})` : ''}`}
                />

                <span
                    className='vntv-live-screen'
                    aria-hidden={true}
                >
                    <Backdrop
                        userId={stream.userId}
                        picture={picture}
                    />
                    {stream.alwaysOn && !still && picture ? (
                        <img
                            className='vntv-live-album'
                            src={picture}
                            alt=''
                            draggable={false}
                        />
                    ) : null}
                    {still ? <Crossfade src={still}/> : null}
                </span>

                <span
                    className='vntv-live-tags'
                    aria-hidden={true}
                >
                    {stream.alwaysOn ? (
                        <span
                            className='vntv-live-tag'
                            data-tone='always'
                        >{'24/7'}</span>
                    ) : (
                        <span
                            className='vntv-live-tag'
                            data-tone={stream.paused ? 'paused' : 'live'}
                        >
                            {stream.paused ? null : <span className='vntv-live-pulse'/>}
                            {stream.paused ? 'Paused' : 'Live'}
                        </span>
                    )}
                    {/* Being watched is a quiet tag beside LIVE, not a
                        ring and glow round the card. The button's pressed
                        state says it aloud. */}
                    {watching ? (
                        <span
                            className='vntv-live-tag'
                            data-tone='watching'
                        >
                            <EyeIcon size={11}/>
                            <span className='vntv-live-tag-word'>{'Watching'}</span>
                        </span>
                    ) : null}
                    {/* The plain word, never Twitch's logo or its purple,
                        with Twitch's whole audience beside it — even none:
                        a count there is always Twitch's, never people in
                        this panel. */}
                    {twitch ? (
                        <span
                            className='vntv-live-tag vntv-card-tag-twitch'
                            title={watchingOnTwitchWords(stream.viewers)}
                        >
                            {'Twitch'}
                            <span className='vntv-card-tag-count'>
                                <EyeIcon size={11}/>
                                {Math.max(0, stream.viewers).toLocaleString()}
                            </span>
                        </span>
                    ) : null}
                    {uptime ? (
                        <span
                            className='vntv-live-tag vntv-live-uptime'
                            title={`Live for ${uptime}`}
                        >{uptime}</span>
                    ) : null}
                    {faces.length > 0 ? (
                        <span
                            className='vntv-live-tag vntv-live-watchers'
                            title={`Watching: ${watchedBy}`}
                        >
                            <span className='vntv-live-crew'>
                                {faces.map((person) => (
                                    <Avatar
                                        key={person.userId}
                                        className='vntv-live-crew-face'
                                        userId={person.userId}
                                        name={person.who}
                                        decorative={true}
                                    />
                                ))}
                            </span>
                            {unseen > 0 ? `+${unseen}` : null}
                        </span>
                    ) : null}
                    {!twitch && faces.length === 0 && stream.viewers > 0 ? (
                        <span
                            className='vntv-live-tag'
                            title={`${stream.viewers} watching`}
                        >
                            <EyeIcon size={11}/>
                            {stream.viewers}
                        </span>
                    ) : null}
                </span>

                <span className='vntv-live-info'>
                    {/* What is on, on a line of its own above the name: on
                        the picture rather than among the name, the medals
                        and the title, which already fill their lines. It is
                        dressed as one more of the tags along the top, so it
                        reads as part of the card and stays readable on a
                        bright picture. The info is anchored to the bottom,
                        so this only ever rises into the picture and never
                        pushes them. */}
                    {onNow ? (
                        <OnNow
                            nowPlaying={onNow}
                            who={stream.title}
                            className='vntv-live-onnow'
                            onWatch={watching ? undefined : () => onWatchOnly(stream.handle)}
                        />
                    ) : null}
                    {category ? (
                        <span
                            className='vntv-onnow vntv-live-onnow vntv-twitch-category'
                            title='Twitch category'
                        >
                            <span className='vntv-onnow-title'>{category}</span>
                        </span>
                    ) : null}
                    <span className='vntv-live-who'>
                        <Avatar
                            className='vntv-live-face'
                            userId={stream.userId}
                            src={picture}
                            name={who}
                            decorative={true}
                        />
                        <span
                            className='vntv-live-name'
                            title={stream.title}
                        >{stream.title}</span>
                        {voice ? (
                            <span
                                className='vntv-medal'
                                role='img'
                                aria-label={voiceLabel(voice)}
                                title={voiceLabel(voice)}
                            ><VoiceBadge
                                muted={voice.muted}
                                size={BADGE_SIZE}
                            /></span>
                        ) : null}
                        {badges.map((award) => (
                            <AwardBeside
                                key={award.id}
                                className='vntv-medal'
                                award={award}
                                size={BADGE_SIZE}
                            />
                        ))}
                    </span>
                    {words !== '' || (note === '' && stream.username) ? (
                        <span
                            className='vntv-live-note'
                            title={note || undefined}
                        >{words || `@${stream.username}`}</span>
                    ) : null}
                    {fan ? (

                        // Who has watched them most, quietly, under the
                        // title: the fan's face in a gold ring, then
                        // "Biggest fan · Bob". On a card with room, a line
                        // of its own. On the small two-up card, the same face
                        // as "Fan · Bob" at the end of the links line, where
                        // it gives way to the links: under them when they
                        // need the line and the card has one to spare, and
                        // not at all when it has none. A name, a hashtag or
                        // an address is never cut for it. Never pressable:
                        // the profile says more.
                        <span className='vntv-live-tail'>
                            {linksLine}
                            <span
                                className='vntv-live-fan'
                                title={`${FAN_TITLE}: ${fan.fan} · ${hoursInWords(fan.hours)} watching`}
                                aria-hidden={true}
                            >
                                <Avatar
                                    className='vntv-live-fan-face'
                                    userId={fan.fanId}
                                    name={fan.fan}
                                    decorative={true}
                                />
                                <span className='vntv-live-fan-words'>
                                    <span className='vntv-live-fan-label'>{`${FAN_TITLE} · `}</span>
                                    <span className='vntv-live-fan-short'>{'Fan · '}</span>
                                    <span className='vntv-live-fan-name'>{fan.fan}</span>
                                </span>
                            </span>
                        </span>
                    ) : linksLine}
                </span>
            </div>

            {hasButton ? (
                <button
                    type='button'
                    className='vntv-live-add'
                    aria-label={addLabel}
                    title={addLabel}
                    disabled={!watching && full}
                    onClick={() => onToggle(stream.handle)}
                    onFocus={() => {
                        addFocused.current = true;
                    }}
                    onBlur={() => {
                        addFocused.current = false;
                    }}
                >{watching ? <MinusIcon size={13}/> : <PlusIcon size={13}/>}</button>
            ) : null}
        </li>
    );
};

// What the picture area shows before, or instead of, a still: the streamer's
// own picture, blurred into a wash of its colours, so every card has a tint of
// its own and none is ever an empty black box. Initials, where there is no
// picture, get the initials' own gradient from the stylesheet.
//
// By user id rather than by stream, so a clip with no picture of its own can
// wear its streamer's the same way, long after the stream has gone. It fills
// whatever positioned box it is put in.
export const Backdrop: React.FC<{userId?: string; picture?: string}> = ({userId, picture}) => {
    const url = picture || (userId ? `/api/v4/users/${encodeURIComponent(userId)}/image` : '');
    const [failed, setFailed] = React.useState(false);

    React.useEffect(() => setFailed(false), [url]);

    if (!url || failed) {
        return <span className='vntv-live-wash'/>;
    }

    return (
        <img
            className='vntv-live-blur'
            src={url}
            alt=''
            draggable={false}
            onError={() => setFailed(true)}
        />
    );
};

type Layer = {src: string; shown: boolean};

// A new still fades in over the old one rather than replacing it with a flash
// of the backdrop: the new picture is loaded underneath the eye, shown once it
// has arrived, and the old one taken away only when it is covered. A still
// that fails to load is dropped, and whatever was showing stays.
const Crossfade: React.FC<{src: string}> = ({src}) => {
    const [layers, setLayers] = React.useState<Layer[]>([]);

    // Pictures already on screen are kept until the new one covers them; one
    // still loading when a newer one arrives is simply replaced by it.
    React.useEffect(() => {
        setLayers((current) => (current.some((layer) => layer.src === src) ? current : [...current.filter((layer) => layer.shown).slice(-2), {src, shown: false}]));
    }, [src]);

    React.useEffect(() => {
        const newest = layers[layers.length - 1];
        if (!newest?.shown || layers.length < 2) {
            return undefined;
        }

        const timer = window.setTimeout(() => setLayers((current) => current.slice(-1)), CROSSFADE_MS);

        return () => window.clearTimeout(timer);
    }, [layers]);

    return (
        <>
            {layers.map((layer) => (
                <img
                    key={layer.src}
                    className='vntv-live-layer'
                    data-shown={layer.shown ? 'true' : undefined}
                    src={layer.src}
                    alt=''
                    draggable={false}
                    onLoad={() => setLayers((current) => current.map((item) => (item.src === layer.src ? {...item, shown: true} : item)))}
                    onError={() => setLayers((current) => current.filter((item) => item.src !== layer.src))}
                />
            ))}
        </>
    );
};
