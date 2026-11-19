import React from 'react';

import {joinPlan, mediaImageURL, unplanStream} from '../client';
import {onPlanFocus, takePlanFocus} from '../planLink';
import {clockTime, formatWhen, leadWhen, viewerZone} from '../time';
import {watchHandle} from '../twitch';
import type {ScheduleItem} from '../types';

import {Avatar} from './Avatar';
import {categoryOf, CategoryIcon} from './categories';
import {ChevronIcon, CloseIcon, PencilIcon, ShareIcon} from './icons';
import {MediaProfile} from './MediaProfile';
import {useShare} from './ShareClipButton';
import {reducedMotion} from '../motion';

type Props = {
    items: ScheduleItem[];

    // Who is on air this second, by user id, carrying the handle their stream
    // is addressed by. A card whose time has come says so instead of counting
    // up from a start that already happened, and clicking it opens that stream.
    liveHandles: Record<string, string>;

    meUserId?: string;
    isAdmin?: boolean;
    onChanged: () => void;

    // Opens the planning form, in the footer, on one of your own plans,
    // filled in, to change it; the pencil it was opened from goes with it,
    // for focus to come back to.
    onEdit?: (item: ScheduleItem, from: HTMLButtonElement) => void;

    // Whether the viewer can say they are in on somebody's plan: only with a
    // stream of their own linked, since joining means streaming in it.
    canJoin?: boolean;

    // Whether the viewer can change a plan of theirs: the same rule as
    // joining, and the server holds to it. Without it there is no pencil on
    // their own plans. The × on those stays: somebody who planned something
    // and has since unlinked can still take it down.
    canPlan: boolean;

    // Whether the viewer can share a plan into the channel they have open
    // (design/3.2.0.md 6.6): linked, as the server holds it. Any listed
    // plan, anybody's.
    canShare?: boolean;
};

// Where a share from one card stands, said once under the rail rather than
// squeezed into a card 168px wide: "Click again to post it in Town Square",
// "Sharing…", "Shared in Town Square.", or why it did not go.
type ShareLine = {id: string; text: string; tone: 'armed' | 'busy' | 'done' | 'error'};

// The share glyph on a card, beside the pencil and the ×, revealed the same
// way. Two presses, as every share takes (twoClicks.ts): the first turns it
// red and says where it will go, under the rail. slot is how many tools sit
// to its right.
const ShareTool: React.FC<{item: ScheduleItem; slot: number; onLine: (line: ShareLine | null, id: string) => void}> = ({item, slot, onLine}) => {
    const share = useShare({kind: 'plan', id: item.id});
    let phase: '' | 'armed' | 'sharing' | 'shared' = '';
    if (share.words === 'Sharing…') {
        phase = 'sharing';
    } else if (share.words === 'Shared') {
        phase = 'shared';
    } else if (share.armed) {
        phase = 'armed';
    }

    const where = share.target.label.where;
    const failure = share.failure?.text || '';
    React.useEffect(() => {
        if (phase === 'armed') {
            onLine({id: item.id, text: share.words, tone: 'armed'}, item.id);
        } else if (phase === 'sharing') {
            onLine({id: item.id, text: 'Sharing…', tone: 'busy'}, item.id);
        } else if (failure !== '') {
            onLine({id: item.id, text: failure, tone: 'error'}, item.id);
        } else if (phase === 'shared' && share.done !== '') {
            onLine({id: item.id, text: share.done, tone: 'done'}, item.id);
        } else {
            onLine(null, item.id);
        }
    }, [phase, failure, share.done, share.words, item.id, onLine]);

    return (
        <button
            ref={share.button}
            type='button'
            className='vntv-upnext-share'
            data-slot={slot}
            data-phase={phase || undefined}
            aria-label={phase === '' ? `Share ${item.title} ${where}` : share.words}
            aria-disabled={share.disabled ? true : undefined}
            title={share.title || `Share this plan ${where}`}
            onClick={share.press}
        ><ShareIcon size={12}/></button>
    );
};

// Faces shown in a row before the rest become "+N".
const FACES = 3;

// Ids for the profile, unique per rail, from a counter; see TitleSearch.
let nextProfileId = 0;

// An hour out is the point at which "later" becomes "soon" and somebody might
// go and make a cup of tea about it.
const IMMINENT_MS = 3600000;

// Where a card's left edge comes to rest when an arrow moves the rail: clear of
// the back arrow, which sits at the rail's very edge, and of the fade under
// it. Must agree with the fade in the stylesheet.
const STEP_LANDING = 40;

// The rail: what is coming, soonest on the left.
//
// Only while something is planned (Sidebar decides that, since it decides
// where the rail goes). An empty queue used to keep the row on screen, as
// placeholders with a + at their start, which was a whole row of the panel
// spent on nothing; Victor asked for it to go. Planning now starts from the
// calendar in the footer's bar, which is there whether or not anything is
// planned, and opens the form there; the plan it saves arrives here.
//
// Three lines per card, in the order somebody actually asks the questions:
// when, what, who. The lede is a countdown while a countdown is useful and a
// day name after that (see leadWhen); the clock time sits on the last line so
// it is always available without the lede having to carry two ideas.
export const UpNext: React.FC<Props> = ({items, liveHandles, meUserId, isAdmin, onChanged, onEdit, canJoin, canPlan, canShare}) => {
    const zone = viewerZone();
    const [busy, setBusy] = React.useState('');

    // The one share line under the rail: the newest card's to say anything.
    // A card that falls quiet takes only its own line away.
    const [shareLine, setShareLine] = React.useState<ShareLine | null>(null);
    const onShareLine = React.useCallback((line: ShareLine | null, id: string) => {
        setShareLine((was) => {
            if (line) {
                return was && was.id === line.id && was.text === line.text && was.tone === line.tone ? was : line;
            }

            return was && was.id === id ? null : was;
        });
    }, []);
    const busyRef = React.useRef('');
    const [error, setError] = React.useState('');

    // The one profile open, if any: which plan, where its card is, and whether
    // the keyboard opened it (then focus goes into it).
    const [profile, setProfile] = React.useState<{id: string; card: HTMLElement; keyboard: boolean} | null>(null);
    const openTimer = React.useRef(0);
    const closeTimer = React.useRef(0);
    // A touch press on a linked card: whether it began one, and whether the
    // profile was already open when it did. The press focuses the card, and
    // focus opens the profile, before the tap's click arrives — so the click
    // has to go by how things were when the finger went down.
    const touchPress = React.useRef(false);
    const pressWasOpen = React.useRef(false);

    // Set while focus is being put back on a card, so that doing it does not
    // open the profile that was just closed.
    const returningFocus = React.useRef(false);
    const [profileId] = React.useState(() => {
        nextProfileId += 1;

        return `villanet-profile-${nextProfileId}`;
    });

    const cancelProfileTimers = React.useCallback(() => {
        window.clearTimeout(openTimer.current);
        window.clearTimeout(closeTimer.current);
    }, []);

    const showProfile = React.useCallback((id: string, card: HTMLElement, keyboard = false) => {
        cancelProfileTimers();
        setProfile({id, card, keyboard});
    }, [cancelProfileTimers]);

    const hideProfile = React.useCallback(() => {
        cancelProfileTimers();
        setProfile(null);
    }, [cancelProfileTimers]);

    // Escape from inside the profile: close it and give the card focus back —
    // the card's own button when it is live, the card otherwise — rather than
    // letting focus fall out of the panel with the profile.
    const closeProfileToCard = React.useCallback((card: HTMLElement) => {
        hideProfile();
        const target = card.querySelector<HTMLElement>('.vntv-upnext-open') || card;
        returningFocus.current = true;
        target.focus();
        returningFocus.current = false;
    }, [hideProfile]);

    // Pointing: a moment's pause before it opens, so sweeping the mouse across
    // the rail does not flicker a profile up for every card it crosses; and a
    // moment before it closes, so the pointer can travel from the card into
    // the profile to reach its link.
    const hoverIn = (id: string, card: HTMLElement) => {
        window.clearTimeout(closeTimer.current);
        window.clearTimeout(openTimer.current);
        openTimer.current = window.setTimeout(() => showProfile(id, card), 320);
    };

    const hoverOut = () => {
        window.clearTimeout(openTimer.current);
        window.clearTimeout(closeTimer.current);
        closeTimer.current = window.setTimeout(() => setProfile(null), 180);
    };

    React.useEffect(() => cancelProfileTimers, [cancelProfileTimers]);

    // The profile follows its card as the rail moves; it closes once the card
    // has gone under the controls at the left or off the right-hand end.
    const profileStillInView = React.useCallback(() => {
        const rail = railRef.current;
        if (!profile || !rail) {
            return;
        }

        const card = profile.card.getBoundingClientRect();
        const box = rail.getBoundingClientRect();
        const middle = card.left + (card.width / 2);
        if (middle < box.left + STEP_LANDING || middle > box.right) {
            setProfile(null);
        }
    }, [profile]);

    // Where the rail is along its length: at its start, at its end, both (it
    // all fits) or neither. That decides which arrows exist, and which edges
    // fade. Only replaced when one of the two actually changes, so doing this
    // on every scroll event does not re-render on every scroll event.
    const railRef = React.useRef<HTMLUListElement | null>(null);
    const [edges, setEdges] = React.useState({start: true, end: true});

    const measure = React.useCallback(() => {
        const rail = railRef.current;
        if (!rail) {
            return;
        }

        const furthest = rail.scrollWidth - rail.clientWidth;
        const start = rail.scrollLeft <= 4;
        const end = rail.scrollLeft >= furthest - 4;

        setEdges((current) => (current.start === start && current.end === end ? current : {start, end}));
    }, []);

    // The ledes are counting down, so they have to move without waiting for a
    // poll. Half a minute is under the resolution of everything on the card.
    const [now, setNow] = React.useState(() => Date.now());
    React.useEffect(() => {
        const timer = window.setInterval(() => setNow(Date.now()), 30000);

        return () => window.clearInterval(timer);
    }, []);

    // The server already sorts these, and sorts them with a tie-break so the
    // order is total. Sorted again here because the rail's whole meaning is its
    // order: a snapshot that arrived in some other order would not look wrong,
    // it would silently lie about what is next.
    const ordered = React.useMemo(
        () => items.slice().sort((a, b) => (a.startsAt - b.startsAt) || (a.id < b.id ? -1 : 1)),
        [items],
    );

    // A plan link followed (planLink.ts): that plan's card brought into view
    // and focused, once it is on the rail. A card is not otherwise a tab stop
    // unless it opens a profile, so it is given a place in the focus order
    // that only a script can take.
    const [focusAsked, setFocusAsked] = React.useState(0);
    React.useEffect(() => {
        onPlanFocus(() => setFocusAsked((count) => count + 1));

        return () => onPlanFocus(null);
    }, []);
    React.useEffect(() => {
        const id = takePlanFocus(ordered.map((item) => item.id));
        const card = id ? railRef.current?.querySelector<HTMLElement>(`[data-plan="${id}"]`) : null;
        if (!card) {
            return;
        }

        card.scrollIntoView({block: 'nearest', inline: 'center'});
        if (card.tabIndex < 0 && !card.hasAttribute('tabindex')) {
            card.setAttribute('tabindex', '-1');
        }
        card.focus({preventScroll: true});
    }, [ordered, focusAsked]);

    // An error from a cancel that failed has no other way out: this component
    // never unmounts while the panel is open, and nothing else touches the
    // message. Clear it when the schedule changes — which is what happens when
    // the cancel finally works, or when somebody else's change arrives.
    React.useEffect(() => setError(''), [items]);

    // The panel can be resized, popped out, or narrowed by Mattermost around
    // it, and the cards change as plans come and go — any of which moves the
    // ends of the rail without anybody scrolling.
    React.useLayoutEffect(measure, [measure, ordered.length]);
    React.useEffect(() => {
        const rail = railRef.current;
        if (!rail || typeof ResizeObserver === 'undefined') {
            return undefined;
        }

        const observer = new ResizeObserver(measure);
        observer.observe(rail);

        return () => observer.disconnect();
    }, [measure]);

    // One event at a time. Each card has a resting place — its left edge just
    // clear of the controls over the rail — and the arrows move between those,
    // rather than scrolling by a fixed amount and leaving a card cut in half at
    // the left.
    const step = React.useCallback((direction: 1 | -1) => {
        const rail = railRef.current;
        if (!rail) {
            return;
        }

        const furthest = rail.scrollWidth - rail.clientWidth;
        const stops = [...rail.querySelectorAll<HTMLElement>('.vntv-upnext-card')].map((card, index) => (
            index === 0 ? 0 : Math.min(furthest, Math.max(0, card.offsetLeft - STEP_LANDING))
        ));
        if (stops.length === 0) {
            return;
        }

        const at = rail.scrollLeft;

        // The last resting place at or before here. Going back from between
        // two stops goes to the earlier one first, not two back.
        let here = 0;
        stops.forEach((stop, index) => {
            if (stop <= at + 2) {
                here = index;
            }
        });

        const between = at > stops[here] + 2;
        const next = direction > 0 ?
            Math.min(stops.length - 1, here + 1) :
            (between ? here : Math.max(0, here - 1));

        // Past the last card that can be a resting place, forward means the end.
        const target = direction > 0 && next === here ? furthest : stops[next];

        rail.scrollTo({left: target, behavior: reducedMotion() ? 'auto' : 'smooth'});
    }, []);

    // In, or out again. The same busy flag as cancelling: one change to the
    // schedule at a time from this rail.
    const toggleJoin = React.useCallback((id: string, join: boolean) => {
        if (busyRef.current) {
            return;
        }
        busyRef.current = id;
        setBusy(id);
        setError('');

        joinPlan(id, join).then(onChanged).catch((reason: unknown) => {
            setError(reason instanceof Error ? reason.message : String(reason));
        }).finally(() => {
            busyRef.current = '';
            setBusy('');
        });
    }, [onChanged]);

    const remove = React.useCallback((id: string) => {
        setBusy(id);
        setError('');

        unplanStream(id).then(onChanged).catch((reason: unknown) => {
            setError(reason instanceof Error ? reason.message : String(reason));
        }).finally(() => setBusy(''));
    }, [onChanged]);

    return (
        <div className='vntv-upnext'>
            <p className='vntv-upnext-eyebrow'>{'Up next'}</p>

            {error !== '' && <p className='vntv-hint vntv-notice-error'>{error}</p>}

            {/* The rail, and the arrows OVER it rather than in it: the rail
                fades its own edges — that is how a card seems to slide under
                an arrow rather than being cut off by it — and anything inside
                the rail would fade with it. */}
            <div
                className='vntv-upnext-track'
                data-start={edges.start ? 'true' : undefined}
                data-end={edges.end ? 'true' : undefined}
            >
                <ul
                    ref={railRef}
                    className='vntv-upnext-rail'
                    tabIndex={0}
                    aria-label='Upcoming streams'
                    onScroll={() => {
                        measure();
                        profileStillInView();
                    }}
                >
                    {ordered.map((item, index) => {
                        const away = item.startsAt - now;

                        // Being on air only speaks for a plan whose time has come.
                        // Somebody streaming tonight has not started tomorrow's
                        // session, and labelling both of their cards "Live now"
                        // makes the rail say the one thing it must never say —
                        // that a thing is happening when it is not.
                        const handle = (item.userId && liveHandles[item.userId]) || '';
                        const live = away <= 0 && handle !== '';
                        const mine = Boolean(item.userId && item.userId === meUserId);

                        // Changing a plan takes a linked stream, like making
                        // one; cancelling it, below, never does.
                        const changeable = mine && canPlan && Boolean(onEdit);
                        const kind = categoryOf(item.category);
                        const media = item.media;
                        const open = profile?.id === item.id;
                        const joined = item.joined || [];
                        const amIn = Boolean(meUserId && joined.some((person) => person.userId === meUserId));

                        // One joiner's face and a count when the Join pill
                        // shares the line — that is all a card has room for
                        // without squeezing them — and up to three without it.
                        const faces = (canJoin || amIn) && !mine ? 1 : FACES;

                        // The tools in the top corner, right to left: the ×,
                        // the pencil, the share glyph.
                        const removable = mine || Boolean(isAdmin);
                        const tools = Number(removable) + Number(changeable && Boolean(onEdit)) + Number(Boolean(canShare));

                        // A linked card opens its profile: pointed at, focused,
                        // or — on a touch screen, which cannot point — tapped.
                        // A tap on a live one opens the profile first, and the
                        // profile has the Watch button.
                        const linkedHandlers = media ? {
                            tabIndex: live ? undefined : 0,
                            'aria-describedby': open ? profileId : undefined,
                            onPointerEnter: (event: React.PointerEvent<HTMLLIElement>) => {
                                if (event.pointerType === 'mouse') {
                                    hoverIn(item.id, event.currentTarget);
                                }
                            },
                            onPointerLeave: (event: React.PointerEvent<HTMLLIElement>) => {
                                if (event.pointerType === 'mouse') {
                                    hoverOut();
                                }
                            },
                            onPointerDown: (event: React.PointerEvent<HTMLLIElement>) => {
                                touchPress.current = event.pointerType !== 'mouse';
                                pressWasOpen.current = open;
                            },
                            onClickCapture: (event: React.MouseEvent<HTMLLIElement>) => {
                                const tapped = touchPress.current;
                                touchPress.current = false;
                                if (!tapped || event.detail === 0) {
                                    return;
                                }
                                if ((event.target as Element).closest('.vntv-upnext-remove, .vntv-upnext-edit, .vntv-upnext-share, .vntv-upnext-join')) {
                                    return;
                                }

                                // A tap opens the profile — even on a live card,
                                // whose profile has Watch now — and a tap on a
                                // card whose profile is open closes it again.
                                if (!pressWasOpen.current) {
                                    event.preventDefault();
                                    event.stopPropagation();
                                    showProfile(item.id, event.currentTarget);
                                } else if (!live) {
                                    hideProfile();
                                }
                            },
                            onFocus: (event: React.FocusEvent<HTMLLIElement>) => {
                                // A tap focuses the card on its way to a click,
                                // and the click decides; so does putting focus
                                // back after Escape.
                                if (touchPress.current || returningFocus.current) {
                                    return;
                                }
                                if (event.target === event.currentTarget || (event.target as Element).classList.contains('vntv-upnext-open')) {
                                    showProfile(item.id, event.currentTarget);
                                }
                            },
                            onBlur: (event: React.FocusEvent<HTMLLIElement>) => {
                                const next = event.relatedTarget as Node | null;
                                const inside = next && (event.currentTarget.contains(next) || document.getElementById(profileId)?.contains(next));
                                if (!inside) {
                                    hideProfile();
                                }
                            },
                            onKeyDown: (event: React.KeyboardEvent<HTMLLIElement>) => {
                                const onCard = event.target === event.currentTarget;
                                const onOpen = (event.target as Element).classList.contains('vntv-upnext-open');

                                if (event.key === 'Escape' && open) {
                                    event.stopPropagation();
                                    hideProfile();
                                } else if (((event.key === 'Enter' || event.key === ' ') && onCard) || (event.key === 'ArrowDown' && (onCard || onOpen))) {
                                    // Into the profile, to reach its link. Down
                                    // is the way in from a live card, whose
                                    // Enter watches.
                                    event.preventDefault();
                                    showProfile(item.id, event.currentTarget, true);
                                }
                            },
                        } : {};

                        return (
                            <li
                                key={item.id}
                                className='vntv-upnext-card'
                                data-plan={item.id}
                                data-lead={index === 0}
                                data-live={live}
                                data-soon={!live && away <= IMMINENT_MS}
                                data-media={media ? 'true' : undefined}
                                data-own={changeable ? 'true' : undefined}
                                data-tools={tools > 0 ? tools : undefined}
                                data-sharing={shareLine?.id === item.id && shareLine.tone !== 'done' ? 'true' : undefined}
                                data-open={open ? 'true' : undefined}
                                title={media ? undefined : `${kind ? kind.label + ': ' : ''}${item.title} — ${item.who}, ${formatWhen(item.startsAt, zone, now)}`}
                                aria-label={media ? `${item.title}, ${item.who}, ${formatWhen(item.startsAt, zone, now)}. ${media.title}${media.year ? ` (${media.year})` : ''}.` : undefined}
                                {...linkedHandlers}
                            >
                                {/* The movie's or game's own art, faded in behind
                                    the words, darkest where the words are. */}
                                {media && (
                                    <CardArt
                                        key={media.ref}
                                        mediaRef={media.ref}
                                    />
                                )}

                                <span className='vntv-upnext-lede'>
                                    {live && (
                                        <span
                                            className='vntv-upnext-dot'
                                            aria-hidden={true}
                                        />
                                    )}
                                    {live ? 'Live now' : leadWhen(item.startsAt, zone, now)}
                                </span>
                                <span className='vntv-upnext-title'>
                                    <CategoryIcon
                                        category={item.category}
                                        className='vntv-upnext-kind'
                                    />
                                    <span className='vntv-upnext-what'>{item.title}</span>
                                </span>
                                {/* Who, as a face rather than a name: a face is
                                    recognised at a glance and a name has to be
                                    read. The name is still there for anybody
                                    who cannot see the picture — it is the
                                    picture's alt text, and in the card's
                                    tooltip. */}
                                <span className='vntv-upnext-who'>
                                    <Avatar
                                        className='vntv-upnext-avatar'
                                        userId={item.userId}
                                        name={item.who}
                                    />

                                    {/* Who else is in: their faces tucked in
                                        behind the host's, the first few and
                                        then a count. */}
                                    {joined.length > 0 && (
                                        <span className='vntv-upnext-crew'>
                                            {joined.slice(0, faces).map((person) => (
                                                <Avatar
                                                    key={person.userId}
                                                    className='vntv-upnext-avatar vntv-upnext-crew-face'
                                                    userId={person.userId}
                                                    name={`${person.who} is in`}
                                                />
                                            ))}
                                            {joined.length > faces && (
                                                <span
                                                    className='vntv-upnext-crew-more'
                                                    title={joined.slice(faces).map((person) => person.who).join(', ')}
                                                >{`+${joined.length - faces}`}</span>
                                            )}
                                        </span>
                                    )}

                                    <span className='vntv-upnext-clock'>{clockTime(item.startsAt, zone)}</span>

                                    {/* To join you need a linked stream; to leave,
                                        you only need to be in. */}
                                    {(canJoin || amIn) && !mine && (
                                        <button
                                            type='button'
                                            className='vntv-upnext-join'
                                            aria-pressed={amIn}
                                            aria-label={amIn ? `You are in on ${item.title}. Leave it` : `Join ${item.who} on ${item.title}`}
                                            title={amIn ? 'You are in — press to leave' : 'Say you are in on this one'}
                                            aria-disabled={busy === item.id ? true : undefined}
                                            onClick={() => {
                                                if (busy !== item.id) {
                                                    toggleJoin(item.id, !amIn);
                                                }
                                            }}
                                        >{amIn ? 'In ✓' : 'Join'}</button>
                                    )}
                                </span>

                                {/* The whole card is the target, as a transparent
                                    layer over it rather than a button wrapped
                                    round the text — a button inside a button is
                                    not valid, and the cancel × has to stay
                                    clickable. Last but one in the DOM so the ×,
                                    which comes after it, still takes its own
                                    clicks. */}
                                {live && (
                                    <button
                                        type='button'
                                        className='vntv-upnext-open'
                                        aria-label={`Watch ${item.who} now`}
                                        aria-keyshortcuts={media ? 'ArrowDown' : undefined}
                                        onClick={() => watchHandle(handle, 'only')}
                                    />
                                )}

                                {/* Yours to change: the words, the time, the kind,
                                    the title it is linked to. Shown with the ×,
                                    when the card is pointed at or focused, and
                                    always where there is no pointing. */}
                                {changeable && onEdit && (
                                    <button
                                        type='button'
                                        className='vntv-upnext-edit'
                                        aria-label={`Change ${item.title}`}
                                        title='Change this plan'
                                        onClick={(event) => {
                                            hideProfile();
                                            onEdit(item, event.currentTarget);
                                        }}
                                    ><PencilIcon/></button>
                                )}

                                {canShare ? (
                                    <ShareTool
                                        item={item}
                                        slot={tools - 1}
                                        onLine={onShareLine}
                                    />
                                ) : null}

                                {removable && (
                                    <button
                                        type='button'
                                        className='vntv-upnext-remove'
                                        aria-label={`Cancel ${item.title}`}
                                        title={`Cancel ${item.title}`}
                                        disabled={busy === item.id}
                                        onClick={() => remove(item.id)}
                                    ><CloseIcon/></button>
                                )}
                            </li>
                        );
                    })}
                </ul>

                {(() => {
                    const item = profile ? ordered.find((candidate) => candidate.id === profile.id) : undefined;
                    if (!profile || !item?.media) {
                        return null;
                    }

                    const handle = (item.userId && liveHandles[item.userId]) || '';
                    const live = item.startsAt - now <= 0 && handle !== '';

                    return (
                        <MediaProfile
                            key={`${item.id}:${item.media.ref}`}
                            id={profileId}
                            media={item.media}
                            anchor={profile.card}
                            context={`${item.who} · ${formatWhen(item.startsAt, zone, now)}`}
                            onWatch={live ? () => {
                                hideProfile();
                                watchHandle(handle, 'only');
                            } : undefined}
                            host={{userId: item.userId, who: item.who}}
                            joined={item.joined || []}
                            onJoin={(canJoin || (item.joined || []).some((person) => person.userId === meUserId)) && item.userId !== meUserId ?
                                (join: boolean) => toggleJoin(item.id, join) : undefined}
                            amIn={Boolean(meUserId && (item.joined || []).some((person) => person.userId === meUserId))}
                            busy={busy === item.id}
                            onPointerEnter={(event) => {
                                if (event.pointerType === 'mouse') {
                                    window.clearTimeout(closeTimer.current);
                                }
                            }}
                            onPointerLeave={(event) => {
                                if (event.pointerType === 'mouse') {
                                    hoverOut();
                                }
                            }}
                            onClose={() => closeProfileToCard(profile.card)}
                            onFocusLeave={(next) => {
                                if (!next || !profile.card.contains(next)) {
                                    hideProfile();
                                }
                            }}
                            focusInside={profile.keyboard}
                        />
                    );
                })()}

                {/* The arrows, only when there is somewhere to go. They replace
                    the scrollbar, which was a red bar under the cards that
                    belonged to the browser rather than the panel. Swiping and
                    trackpads still scroll the rail as before. */}
                {!edges.start && (
                    <button
                        type='button'
                        className='vntv-upnext-arrow'
                        data-side='back'
                        aria-label='Earlier plans'
                        title='Earlier plans'
                        onClick={() => step(-1)}
                    ><ChevronIcon size={14}/></button>
                )}
                {!edges.end && (
                    <button
                        type='button'
                        className='vntv-upnext-arrow'
                        data-side='forward'
                        aria-label='Later plans'
                        title='Later plans'
                        onClick={() => step(1)}
                    ><ChevronIcon size={14}/></button>
                )}
            </div>

            {/* Always in the page while a share is possible, so what is put
                in it is announced. */}
            {canShare ? (
                <p
                    className='vntv-upnext-shared'
                    role='status'
                    data-tone={shareLine?.tone}
                >{shareLine?.text || ''}</p>
            ) : null}
        </div>
    );
};

// The art behind a linked card. An <img> rather than a background, so a
// picture that will not load simply is not there, and so the browser can wait
// to fetch it until the card is on screen.
const CardArt: React.FC<{mediaRef: string}> = ({mediaRef}) => {
    const [failed, setFailed] = React.useState(false);
    if (failed) {
        return null;
    }

    return (
        <img
            className='vntv-upnext-art'
            src={mediaImageURL(mediaRef, 'backdrop')}
            alt=''
            aria-hidden={true}
            loading='lazy'
            draggable={false}
            onError={() => setFailed(true)}
        />
    );
};
