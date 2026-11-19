import React from 'react';
import {createPortal} from 'react-dom';

import {readyAwardsMine} from '../awardsMine';
import {CLIPPER_LABEL, CLIPPER_TITLE, clipNameOf, clipperScore, upvotesInWords} from '../clipLogic';
import {loadClip, openClips, shownTitleOf, useClip} from '../clips';
import {fanTitleOf, fansHint} from '../fans';
import {formatDuration, ordinal} from '../format';
import {useAccess} from '../identity';
import {displayNameOf, profileOf} from '../mattermost';
import {useName} from '../name';
import {SNOOPER_TITLE} from '../snooper';
import type {BestClipper, Boards, FanView, Leader, MonthMedals, Snapshot} from '../types';
import {useBoardsWatched} from '../useBoards';

import {AchievementsTab} from './AchievementsTab';
import {countMonths, describeTally, newestFirst} from './AwardList';
import {Avatar} from './Avatar';
import {AllTimeBadge, ClipperBadge, FanBadge, MonthBadge, SnooperBadge} from './badges';
import {CloseIcon, PlayIcon, ThumbsUpIcon, TrophyIcon} from './icons';
import {TrophiesYou} from './TrophiesYou';

// What a board's rows are, for the line under its heading: "4 streamers",
// "1 snooper".
type Noun = {one: string; many: string};

const STREAMERS: Noun = {one: 'streamer', many: 'streamers'};
// The owner named the watching board Top Snoopers; the code keeps calling
// it watch time, which is what it measures.
const WATCHERS: Noun = {one: 'snooper', many: 'snoopers'};

type BoardProps = {
    heading: string;
    hint?: string;
    leaders: Leader[];
    total: number;
    meUserId?: string;
    noun: Noun;

    // The medal artwork for the podium. Without one every place is a plain
    // number, as on the Snoopers' month board: watching wins one award, all
    // time, and nothing by the month.
    Badge?: React.FC<{rank: number; size?: number}>;

    // How many places wear it: the podium of three for airtime, and first
    // place alone for the Top Snooper, whose award has one holder.
    places?: number;

    // What the badge is called, when it has no digit to give the place:
    // read out after the place, and shown when it is pointed at.
    award?: string;

    // Across the whole card, for a board with no other beside it; see
    // .vntv-lb-wide.
    wide?: boolean;
};

// Every streamer's hours, not just the podium. The hours were always recorded
// for everybody — only the display was cut to three — so this shows what was
// already there.
//
// The AWARD stays at three. A badge everybody has is not an award, so fourth
// place onwards gets a plain number in the same column: the row is listed, the
// medal is earned.
//
// The bar is measured against the LEADER, not against the total. Against the
// total, eight streamers produce eight slivers and the chart says nothing;
// against the leader it answers the question people actually have, which is
// how far behind they are.
//
// The Watchers' boards are this same board, without the medals, except for
// the binoculars on the all-time board's first place.
const Board: React.FC<BoardProps> = ({heading, hint, leaders, total, meUserId, noun, Badge, places = 3, award, wide}) => {
    const top = leaders.reduce((most, entry) => Math.max(most, entry.seconds), 0);
    const hours = leaders.reduce((sum, entry) => sum + entry.seconds, 0);

    return (
        <section className={wide ? 'vntv-lb-board vntv-lb-wide' : 'vntv-lb-board'}>
            <header className='vntv-lb-board-head'>
                <h3
                    className='vntv-lb-board-heading'
                    title={hint}
                >{heading}</h3>
                <p className='vntv-lb-board-sub'>
                    {`${total} ${total === 1 ? noun.one : noun.many} · ${formatDuration(hours)} between them`}
                </p>
            </header>

            <ol className='vntv-lb-list'>
                {leaders.map((leader) => (
                    <li
                        key={`${leader.rank}-${leader.title}`}
                        className='vntv-lb-row'
                        data-me={Boolean(meUserId) && leader.userId === meUserId}
                    >
                        <span className='vntv-lb-place'>
                            {Badge && leader.rank <= places ? (
                                <span
                                    role='img'
                                    aria-label={award ? `${ordinal(leader.rank)}, ${award}` : ordinal(leader.rank)}
                                    title={award}
                                >
                                    <Badge
                                        rank={leader.rank}
                                        size={30}
                                    />
                                </span>
                            ) : (
                                <span aria-label={ordinal(leader.rank)}>{leader.rank}</span>
                            )}
                        </span>

                        <span className='vntv-lb-main'>
                            <span className='vntv-lb-line'>
                                <span
                                    className='vntv-lb-name'
                                    title={leader.username ? `@${leader.username}` : leader.title}
                                >{leader.title}</span>
                                <span
                                    className='vntv-lb-hours'

                                    // A person's hours are their Broadcast Box
                                    // and Twitch time together, each second
                                    // once; the card says nothing else about
                                    // where they came from.
                                    title={leader.twitchSeconds && leader.twitchSeconds > 0 ? `incl. ${formatDuration(leader.twitchSeconds)} on Twitch` : undefined}
                                >{formatDuration(leader.seconds)}</span>
                            </span>
                            <span
                                className='vntv-lb-bar'
                                aria-hidden={true}
                            >
                                <span
                                    className='vntv-lb-fill'
                                    style={{width: `${top > 0 ? Math.max(2, (leader.seconds / top) * 100) : 0}%`}}
                                />
                            </span>
                        </span>
                    </li>
                ))}
            </ol>

            {total > leaders.length && (
                <p className='vntv-lb-more'>{`Showing the top ${leaders.length} of ${total}.`}</p>
            )}
        </section>
    );
};

// One person's line of the monthly medals, with each place's months newest
// first: gold, silver, bronze, in that order.
type MedalRow = {
    userId: string;
    username?: string;
    name: string;
    places: string[][];
};

// Most wins first, then the most second places, then thirds — the way a medal
// table is read — and by name after that, so a tie comes out the same for
// everybody rather than in the order the server happened to send it.
function medalRows(tally: MonthMedals[]): MedalRow[] {
    return tally.
        map((entry) => ({
            userId: entry.userId,
            username: entry.username,
            name: entry.displayName || (entry.username ? `@${entry.username}` : 'Unknown streamer'),
            places: [newestFirst(entry.gold), newestFirst(entry.silver), newestFirst(entry.bronze)],
        })).
        filter((row) => row.places.some((months) => months.length > 0)).
        sort((a, b) => (
            (b.places[0].length - a.places[0].length) ||
            (b.places[1].length - a.places[1].length) ||
            (b.places[2].length - a.places[2].length) ||
            a.name.localeCompare(b.name, undefined, {sensitivity: 'base'}) ||
            (a.userId < b.userId ? -1 : 1)
        ));
}

// A count of one place, as the calendar badge and "×N".
//
// Worded by describeTally. For second and third places that is the profile
// popover's own sentence, word for word. Wins read "Champion in 2 months: …"
// here, where the profile folds them into its title row as "Monthly champion,
// 2 months: …"; the months are listed by the same formatter either way, so the
// two never name one month differently. Every month is in it, newest first, and
// none is cut to "+2 more": the count is all the row shows, so the tooltip is
// the only place the months are.
//
// A place somebody never took keeps its column, as a dash, so the three line up
// down the list. It says nothing a screen reader needs, so it is not read out.
const Tally: React.FC<{rank: number; months: string[]}> = ({rank, months}) => {
    if (months.length === 0) {
        return (
            <span
                className='vntv-lb-tally'
                data-none={true}
                aria-hidden={true}
            >{'–'}</span>
        );
    }

    const description = describeTally(rank, months);

    return (
        <span
            className='vntv-lb-tally'
            data-rank={rank}
            role='img'
            aria-label={description}
            title={description}
        >
            <MonthBadge
                rank={rank}
                size={22}
            />
            <span className='vntv-lb-tally-count'>{`×${months.length}`}</span>
        </span>
    );
};

// The podiums of months that have ended, everybody's at once: the same record
// each person's profile shows, in one table. The month board starts again on
// the 1st, so without this a champion was only findable by opening the right
// person's profile.
const MonthlyMedals: React.FC<{tally: MonthMedals[]; meUserId?: string}> = ({tally, meUserId}) => {
    const rows = medalRows(tally);
    if (rows.length === 0) {
        return null;
    }

    // Every month anybody placed in, counted once: three medals a month, so
    // the number of medals would say nothing about how far back this goes.
    const months = new Set<string>();
    rows.forEach((row) => row.places.forEach((list) => list.forEach((month) => months.add(month))));

    return (
        <section className='vntv-lb-board vntv-lb-wide'>
            <header className='vntv-lb-board-head'>
                <h3
                    className='vntv-lb-board-heading'
                    title='Each month’s top three as the board stood when it ended, in UTC'
                >{'Monthly medals'}</h3>
                <p className='vntv-lb-board-sub'>
                    {`Podium finishes in months that have ended · ${countMonths(months.size)}`}
                </p>
            </header>

            <ol className='vntv-lb-list'>
                {rows.map((row) => (
                    <li
                        key={row.userId}
                        className='vntv-lb-row'
                        data-me={Boolean(meUserId) && row.userId === meUserId}
                    >
                        <span
                            className='vntv-lb-name'
                            title={row.username ? `@${row.username}` : row.name}
                        >{row.name}</span>

                        <span className='vntv-lb-tallies'>
                            {row.places.map((list, index) => (
                                <Tally
                                    key={index + 1}
                                    rank={index + 1}
                                    months={list}
                                />
                            ))}
                        </span>
                    </li>
                ))}
            </ol>
        </section>
    );
};

// What the Streamers tab shows: the airtime boards and, under them, the
// monthly medals. Everything the trophy opened before it had tabs.
const StreamersTab: React.FC<{boards: Boards; meUserId?: string}> = ({boards, meUserId}) => {
    const month = boards.month || [];
    const allTime = boards.allTime || [];
    const medals = boards.monthMedals || [];

    // A board on its own spans the card, as it does when nothing is under it.
    const lone = (month.length > 0) !== (allTime.length > 0);

    if (month.length === 0 && allTime.length === 0 && medals.length === 0) {
        return <p className='vntv-lb-empty'>{'Nobody has streamed yet.'}</p>;
    }

    return (
        <div className='vntv-lb-boards'>
            {month.length > 0 && (
                <Board
                    heading={boards.monthLabel || 'This month'}
                    hint='Calendar month in UTC, so the boundary is the same for everyone'
                    leaders={month}
                    total={boards.monthTotal || month.length}
                    meUserId={meUserId}
                    noun={STREAMERS}
                    Badge={MonthBadge}
                    wide={lone}
                />
            )}
            {allTime.length > 0 && (
                <Board
                    heading='All time'
                    leaders={allTime}
                    total={boards.allTimeTotal || allTime.length}
                    meUserId={meUserId}
                    noun={STREAMERS}
                    Badge={AllTimeBadge}
                    wide={lone}
                />
            )}

            {/* Under the boards rather than beside them: what is being won
                now comes first, and what was won in the spring after it. */}
            {medals.length > 0 && (
                <MonthlyMedals
                    tally={medals}
                    meUserId={meUserId}
                />
            )}
        </div>
    );
};

// What the Snoopers tab shows: hours spent watching other people's streams,
// this month and all time, on the same boards as the streamers' but with a
// plain number for every place save one: first place all time wears the Top
// Snooper's binoculars, as it does beside their name everywhere else, and
// that is the board's only award. And a line saying what counts, because the
// board only ever has people with a linked stream on it, and somebody who has
// watched all week without one would otherwise think it had lost them.
const WatchersTab: React.FC<{boards: Boards; meUserId?: string}> = ({boards, meUserId}) => {
    const month = boards.watchMonth || [];
    const allTime = boards.watchAllTime || [];
    const lone = (month.length > 0) !== (allTime.length > 0);

    return (
        <>
            {month.length === 0 && allTime.length === 0 ? (
                <p className='vntv-lb-empty'>{'Nobody has snooped yet.'}</p>
            ) : (
                <div className='vntv-lb-boards'>
                    {month.length > 0 && (
                        <Board
                            heading={boards.monthLabel || 'This month'}
                            hint='Calendar month in UTC, so the boundary is the same for everyone'
                            leaders={month}
                            total={boards.watchMonthTotal || month.length}
                            meUserId={meUserId}
                            noun={WATCHERS}
                            wide={lone}
                        />
                    )}
                    {allTime.length > 0 && (
                        <Board
                            heading='All time'
                            leaders={allTime}
                            total={boards.watchAllTimeTotal || allTime.length}
                            meUserId={meUserId}
                            noun={WATCHERS}
                            Badge={SnooperBadge}
                            places={1}
                            award={SNOOPER_TITLE}
                            wide={lone}
                        />
                    )}
                </div>
            )}

            <p className='vntv-lb-note'>
                {'Time spent watching other people’s streams here, for people who have linked their stream. Several at once count once; your own stream and 24/7 channels never count.'}
            </p>

            {boards.fansOn ? (
                <BiggestFans
                    fans={boards.fans || []}
                    meUserId={meUserId}
                />
            ) : null}
        </>
    );
};

// Under the Snoopers' boards, each streamer's biggest fan (design/3.2.0.md
// 4.5): who, whose, and how many hours, one line each, most hours first,
// reading "Dave · biggest fan · Bob · 41 h" (an approved departure). The
// streamer's face and name, a dim "biggest fan", then the fan, their face in
// the gold ring with the heart they wear by their own name as a pip, as in the
// streamer's profile: so the line reads one way only, where a heart between
// the two names read as either loving the other. A row the reader is in, on
// either side, is marked as theirs, as their row on a board is. No places and
// no bars: each line is somebody's first place, and there is nothing to
// measure it against.
const BiggestFans: React.FC<{fans: FanView[]; meUserId?: string}> = ({fans, meUserId}) => {
    const name = useName();
    const rows = fans.
        slice().
        sort((a, b) => (b.hours - a.hours) || a.streamer.localeCompare(b.streamer, undefined, {sensitivity: 'base'}));

    return (
        <section className='vntv-lb-board vntv-lb-wide vntv-lb-fans'>
            <header className='vntv-lb-board-head'>
                <h3 className='vntv-lb-board-heading'>{'Biggest fans'}</h3>
                <p className='vntv-lb-board-sub'>{fansHint(name)}</p>
            </header>

            {rows.length === 0 ? (
                <p className='vntv-lb-empty'>{'Nobody has watched anybody for an hour yet.'}</p>
            ) : (
                <ol className='vntv-lb-list'>
                    {rows.map((row) => (
                        <li
                            key={row.streamerId}
                            className='vntv-lb-row vntv-lb-fan'
                            data-me={Boolean(meUserId) && (row.fanId === meUserId || row.streamerId === meUserId)}
                            aria-label={`${row.streamer} · biggest fan · ${row.fan} · ${row.hours} ${row.hours === 1 ? 'hour' : 'hours'}`}
                        >
                            <span
                                className='vntv-lb-fan-who'
                                aria-hidden={true}
                            >
                                <Avatar
                                    userId={row.streamerId}
                                    name={row.streamer}
                                    className='vntv-lb-fan-face'
                                    decorative={true}
                                />
                                <span className='vntv-lb-name'>{row.streamer}</span>
                            </span>
                            <span
                                className='vntv-lb-fan-said'
                                aria-hidden={true}
                            >{'biggest fan'}</span>
                            <span
                                className='vntv-lb-fan-who'
                                aria-hidden={true}
                                title={fanTitleOf(row.streamer)}
                            >
                                <span className='vntv-lb-fan-mark'>
                                    <Avatar
                                        userId={row.fanId}
                                        name={row.fan}
                                        className='vntv-lb-fan-face'
                                        decorative={true}
                                    />
                                    <span className='vntv-lb-fan-pip'><FanBadge size={12}/></span>
                                </span>
                                <span
                                    className='vntv-lb-name'
                                    title={row.fanUsername ? `@${row.fanUsername}` : row.fan}
                                >{row.fan}</span>
                            </span>
                            <span
                                className='vntv-lb-hours'
                                aria-hidden={true}
                            >{`${row.hours} h`}</span>
                        </li>
                    ))}
                </ol>
            )}
        </section>
    );
};

// What the Clippers tab shows: the one Best Clipper, all time, by the
// upvotes on their clips — no runner-up, no board — and the holder's best
// clip to watch. The card opens only for somebody who may watch clips (linked,
// or let in by an admin: SidebarTitle), so the clip's title and its upvotes,
// which are clip content, are asked for (GET /clips/clip) only while the tab
// is the one showing; the line is left out if that clip has gone or cannot be
// read.
type ClippersProps = {
    boards: Boards;
    meUserId?: string;

    // Whether this is the tab on show, which is when the best clip is asked
    // for.
    showing: boolean;
    onClose: () => void;

    // Closes the card and opens the Clips dialog on one clip.
    onWatch: (id: string) => void;
};

const ClippersTab: React.FC<ClippersProps> = ({boards, meUserId, showing, onWatch}) => {
    const holder = boards.bestClipper;

    return (
        <section className='vntv-lb-board vntv-lb-wide vntv-lb-clipper'>
            <header className='vntv-lb-board-head'>
                <h3 className='vntv-lb-board-heading'>{CLIPPER_TITLE}</h3>
                <p className='vntv-lb-board-sub'>{'Most upvotes on their clips, all time · a tie keeps the holder'}</p>
            </header>

            {holder ? (
                <ClipperHolder
                    holder={holder}
                    meUserId={meUserId}
                    showing={showing}
                    onWatch={onWatch}
                />
            ) : (
                <p className='vntv-lb-empty'>{'Nobody holds it yet. The upvotes on people’s clips decide it.'}</p>
            )}
        </section>
    );
};

const ClipperHolder: React.FC<Omit<ClippersProps, 'boards' | 'onClose'> & {holder: BestClipper}> = ({holder, meUserId, showing, onWatch}) => {
    const name = holder.displayName || (holder.username ? `@${holder.username}` : 'Somebody');

    return (
        <div
            className='vntv-lb-clipper-holder'
            data-me={Boolean(meUserId) && holder.userId === meUserId}
        >
            <div className='vntv-lb-clipper-row'>
                <span
                    className='vntv-lb-clipper-badge'
                    role='img'
                    aria-label={CLIPPER_LABEL}
                    title={CLIPPER_LABEL}
                ><ClipperBadge size={30}/></span>
                <Avatar
                    userId={holder.userId}
                    name={name}
                    className='vntv-lb-clipper-face'
                    decorative={true}
                />
                <span className='vntv-lb-clipper-who'>
                    <span
                        className='vntv-lb-clipper-name'
                        title={holder.username ? `@${holder.username}` : name}
                    >{name}</span>
                    <span className='vntv-lb-clipper-score'>{clipperScore(holder.upvotes, holder.clips)}</span>
                </span>
            </div>

            {holder.bestClip ? (
                <BestClipLine
                    key={holder.bestClip.id}
                    id={holder.bestClip.id}
                    showing={showing}
                    onWatch={onWatch}
                />
            ) : null}
        </div>
    );
};

// "Best clip: “that jump” (thumbs up) 31 [▶ Watch]". Asked for each time the tab is
// shown, so the count is the one now; until it first answers, and if it
// fails, there is no line at all rather than a guess.
const BestClipLine: React.FC<{id: string; showing: boolean; onWatch: (id: string) => void}> = ({id, showing, onWatch}) => {
    const clip = useClip(id);
    const [state, setState] = React.useState<'loading' | 'ready' | 'hidden'>('loading');

    React.useEffect(() => {
        if (!showing) {
            return undefined;
        }

        let current = true;
        loadClip(id).then((loaded) => {
            if (current) {
                setState(loaded ? 'ready' : 'hidden');
            }
        }).catch(() => {
            // Gone, refused or unreadable: the line is left out. A refusal
            // saying the reader is not linked has already had "me" fetched
            // again (clips.ts), and the tab follows it to the call to link.
            if (current) {
                setState('hidden');
            }
        });

        return () => {
            current = false;
        };
    }, [id, showing]);

    if (state !== 'ready' || !clip) {
        return null;
    }

    const shown = shownTitleOf(clip);

    return (
        <p className='vntv-lb-clipper-best'>
            <span className='vntv-lb-clipper-best-label'>{'Best clip:'}</span>
            <span
                className='vntv-lb-clipper-best-title'
                title={shown}
            >{clip.title !== '' ? `“${clip.title}”` : shown}</span>
            <span
                className='vntv-lb-clipper-best-votes'
                aria-label={upvotesInWords(clip.upvotes)}
            >
                <ThumbsUpIcon size={13}/>
                <span aria-hidden={true}>{clip.upvotes}</span>
            </span>
            <button
                type='button'
                className='vntv-pill vntv-lb-clipper-watch'
                aria-label={`Watch ${clipNameOf(clip)}`}
                onClick={() => onWatch(id)}
            >
                <PlayIcon size={11}/>
                {'Watch'}
            </button>
        </p>
    );
};

type TabId = 'streamers' | 'watchers' | 'clippers' | 'achievements' | 'you';

const TAB_IDS: readonly string[] = ['streamers', 'watchers', 'clippers', 'achievements', 'you'];

type Tab = {
    id: TabId;
    label: string;
};

// Which tab was chosen last, per browser. Only a convenience: storage that is
// switched off, full or private just means the card opens on the first tab.
const TAB_KEY = 'villanet.trophiesTab';

function rememberedTab(): string | null {
    try {
        return window.localStorage.getItem(TAB_KEY);
    } catch {
        return null;
    }
}

function rememberTab(id: TabId): void {
    try {
        window.localStorage.setItem(TAB_KEY, id);
    } catch {
        // Not kept, which costs nothing but opening on the first tab next time.
    }
}

// Ids for the tabs and their panel, unique per card, from a counter (see
// TitleSearch); and not vntv-, which the style check reads as a class name.
let trophiesCards = 0;

type Props = {
    snapshot: Snapshot;
    meUserId?: string;
    onClose: () => void;
};

// The trophies, opened from the trophy in the sidebar header: the Streamers'
// boards and medals, and the Watchers' boards beside them, as tabs.
//
// A portal onto document.body rather than anything inside the panel. Both
// Mattermost's right-hand sidebar header and its title span are `overflow:
// hidden`, so a panel rendered in place would be clipped to a strip; and the
// boards are the one thing here that is genuinely easier to read wide than in a
// 400px column.
//
// The tabs are built from a list, the Clippers' (Best Clipper) the third entry
// on it: the row, the arrow keys and the remembered choice all work from the
// list, and the row has the room.
export const LeaderboardModal: React.FC<Props> = ({snapshot, meUserId, onClose}) => {
    const access = useAccess();

    // The boards are not in the snapshot: they are read as the card opens,
    // if this page's copy is a minute old, and once a minute for as long as
    // it stays open, since nothing else says that the seconds on a board
    // have moved (boards.ts).
    const boards = useBoardsWatched();

    // The reader's own awards, for the You tab, asked as the card opens if
    // this page has none yet, so the tab usually has them by the time it is
    // chosen (awardsMine.ts).
    React.useEffect(() => {
        if (access === 'linked' && meUserId) {
            readyAwardsMine(meUserId, boards.version);
        }
    }, []); // eslint-disable-line react-hooks/exhaustive-deps

    const closer = React.useRef<HTMLButtonElement>(null);
    const tabRow = React.useRef<HTMLDivElement>(null);
    const tabButtons = React.useRef<Partial<Record<TabId, HTMLButtonElement | null>>>({});
    const [ids] = React.useState(() => {
        trophiesCards += 1;

        return `villanet-trophies-${trophiesCards}`;
    });

    // Read through a ref, so that the effect below runs once per opening. The
    // card is re-rendered with every snapshot and every read of the boards,
    // and an effect that ran again each time sent focus to the trophy and
    // back to Close on every refresh, out from under somebody reading a board
    // or moving along the tabs.
    const closeRef = React.useRef(onClose);
    closeRef.current = onClose;

    // What had focus before the card opened — the trophy — which gets it
    // back when the card closes. Only when focus went with the card, though:
    // Watch on the best clip hands it to the Clips dialog, and Link Your
    // Account to the panel's form, and neither is to be taken away again.
    const opener = React.useRef<HTMLElement | null>(null);
    React.useEffect(() => {
        opener.current = document.activeElement as HTMLElement | null;
        closer.current?.focus();

        const onKey = (event: KeyboardEvent) => {
            if (event.key === 'Escape') {
                event.stopPropagation();
                closeRef.current();
            }
        };

        document.addEventListener('keydown', onKey, true);

        return () => {
            document.removeEventListener('keydown', onKey, true);
            const active = document.activeElement;
            if (!active || active === document.body) {
                opener.current?.focus?.();
            }
        };
    }, []);

    // The best clip, in the Clips dialog. Focus goes back to the trophy
    // first, so the dialog — which opens in the same moment — returns it
    // there when it closes, rather than to a Watch button that has gone.
    const watchClip = React.useCallback((id: string) => {
        opener.current?.focus?.();
        closeRef.current();
        openClips(id);
    }, []);

    const streamersHave = (boards.month || []).length > 0 ||
        (boards.allTime || []).length > 0 ||
        (boards.monthMedals || []).length > 0;
    const watchersHave = (boards.watchMonth || []).length > 0 ||
        (boards.watchAllTime || []).length > 0;
    const clippersHave = Boolean(boards.bestClipper);

    // The Snoopers tab is there whenever its board is switched on, with
    // anybody on it or not, so the card can say nobody has watched yet; the
    // Clippers tab likewise whenever Clips are on, or somebody holds Best
    // Clipper. The Streamers tab is always there: it is what the trophy has
    // always opened.
    const tabs: Tab[] = [{id: 'streamers', label: 'Streamers'}];
    if (boards.watchBoards || watchersHave) {
        tabs.push({id: 'watchers', label: 'Snoopers'});
    }
    if (snapshot.clips?.enabled || clippersHave) {
        tabs.push({id: 'clippers', label: 'Clippers'});
    }

    // 3.2.0: Achievements whenever they are on, for everybody who may open
    // the card; You for linked readers, whose own it is (design/3.2.0.md 3.6).
    if ((boards.achievementKinds || []).length > 0) {
        tabs.push({id: 'achievements', label: 'Achievements'});
    }
    if (access === 'linked' && meUserId) {
        tabs.push({id: 'you', label: 'You'});
    }

    // The tab chosen last time, when it is still here; otherwise the first,
    // unless only one of the others has anything to show.
    const [chosen, setChosen] = React.useState<TabId>(() => {
        const remembered = rememberedTab();
        if (remembered && TAB_IDS.includes(remembered)) {
            return remembered as TabId;
        }
        if (streamersHave) {
            return 'streamers';
        }
        if (watchersHave) {
            return 'watchers';
        }

        return clippersHave ? 'clippers' : 'streamers';
    });

    // Recomputed on every snapshot: a tab switched off while the card is open
    // falls back to the first rather than leaving an empty card.
    const active = tabs.some((tab) => tab.id === chosen) ? chosen : tabs[0].id;

    const choose = (id: TabId) => {
        setChosen(id);
        rememberTab(id);
    };

    const activeRef = React.useRef(active);
    activeRef.current = active;

    // Five tabs are wider than a phone's card (422 px with the bundled font,
    // design/3.2.0.md V31), and the row neither wraps nor may cut one off, so
    // it scrolls sideways, with no scrollbar and a soft fade at whichever end
    // has more beyond it; the chosen tab is brought into view when the card
    // opens and whenever the choice moves, by press or by arrow key.
    const [edges, setEdges] = React.useState({start: false, end: false});
    const measureRow = React.useCallback(() => {
        const row = tabRow.current;
        if (!row) {
            return;
        }
        const start = row.scrollLeft > 1;
        const end = row.scrollLeft + row.clientWidth < row.scrollWidth - 1;
        setEdges((current) => (current.start === start && current.end === end ? current : {start, end}));
    }, []);

    React.useEffect(() => {
        window.addEventListener('resize', measureRow);

        return () => window.removeEventListener('resize', measureRow);
    }, [measureRow]);

    React.useLayoutEffect(() => {
        tabButtons.current[active]?.scrollIntoView({block: 'nearest', inline: 'nearest'});
        measureRow();
    }, [active, tabs.length, measureRow]);

    // And again once the display font has arrived, which widens every tab:
    // measured before it, the fades and the scroll were the fallback font's.
    React.useEffect(() => {
        let current = true;
        document.fonts?.ready.then(() => {
            if (current) {
                tabButtons.current[activeRef.current]?.scrollIntoView({block: 'nearest', inline: 'nearest'});
                measureRow();
            }
        });

        return () => {
            current = false;
        };
    }, [measureRow]);

    // The arrow keys move along the row and choose as they go, Home and End
    // go to either end, and only the chosen tab is in the Tab order, as the
    // ARIA tabs pattern has it. Modified keys are left alone: Mattermost has
    // shortcuts of its own on Alt and Ctrl with the arrows.
    const onTabKey = (event: React.KeyboardEvent) => {
        if (event.altKey || event.ctrlKey || event.metaKey || event.shiftKey) {
            return;
        }

        const index = tabs.findIndex((tab) => tab.id === active);
        let next = index;
        switch (event.key) {
        case 'ArrowRight':
            next = (index + 1) % tabs.length;
            break;
        case 'ArrowLeft':
            next = (index - 1 + tabs.length) % tabs.length;
            break;
        case 'Home':
            next = 0;
            break;
        case 'End':
            next = tabs.length - 1;
            break;
        default:
            return;
        }

        event.preventDefault();
        choose(tabs[next].id);
        tabButtons.current[tabs[next].id]?.focus();
    };

    const contentOf = (id: TabId) => {
        switch (id) {
        case 'watchers':
            return (
                <WatchersTab
                    boards={boards}
                    meUserId={meUserId}
                />
            );
        case 'achievements':
            return (
                <AchievementsTab
                    boards={boards}
                    meUserId={meUserId}
                />
            );
        case 'you':

            // Only while it is the tab showing: it asks the server for the
            // reader's own choice and progress as it opens.
            return meUserId && id === active ? (
                <TrophiesYou
                    userId={meUserId}
                    name={displayNameOf(profileOf(meUserId)) || 'You'}
                />
            ) : null;
        case 'clippers':
            return (
                <ClippersTab
                    boards={boards}
                    meUserId={meUserId}
                    showing={id === active}
                    onClose={onClose}
                    onWatch={watchClip}
                />
            );
        default:
            return (
                <StreamersTab
                    boards={boards}
                    meUserId={meUserId}
                />
            );
        }
    };

    // One tab is nothing to switch between, so it gets no row and no tab
    // roles: just its content, as the card was before it had tabs.
    const tabbed = tabs.length > 1;

    return createPortal(
        <div
            className='vntv vntv-modal'
            role='presentation'
            data-tabbed={tabbed}
            onMouseDown={(event) => {
                // Only a press that both starts and ends on the backdrop.
                // Otherwise a text selection dragged out of the card closes it.
                if (event.target === event.currentTarget) {
                    onClose();
                }
            }}
        >
            <div
                className='vntv-modal-card'
                role='dialog'
                aria-modal={true}
                aria-label='Trophies'
                onMouseDown={(event) => event.stopPropagation()}
            >
                <header
                    className='vntv-modal-head'
                    data-tabbed={tabbed}
                >
                    <span
                        className='vntv-modal-icon'
                        aria-hidden={true}
                    ><TrophyIcon size={20}/></span>
                    <h2 className='vntv-modal-title'>{'Trophies'}</h2>
                    <button
                        ref={closer}
                        type='button'
                        className='vntv-modal-close'
                        aria-label='Close'
                        onClick={onClose}
                    ><CloseIcon size={16}/></button>
                </header>

                {tabbed && (
                    <div
                        ref={tabRow}
                        className='vntv-trophy-tabs'
                        role='tablist'
                        aria-label='Trophies'
                        data-more-start={edges.start ? 'true' : undefined}
                        data-more-end={edges.end ? 'true' : undefined}
                        onKeyDown={onTabKey}
                        onScroll={measureRow}
                    >
                        {tabs.map((tab) => (
                            <button
                                key={tab.id}
                                ref={(element) => {
                                    tabButtons.current[tab.id] = element;
                                }}
                                id={`${ids}-tab-${tab.id}`}
                                type='button'
                                role='tab'
                                className='vntv-trophy-tab'
                                aria-selected={tab.id === active}
                                aria-controls={`${ids}-panel-${tab.id}`}
                                tabIndex={tab.id === active ? 0 : -1}
                                onClick={() => choose(tab.id)}
                            >{tab.label}</button>
                        ))}
                    </div>
                )}

                {/* A panel for every tab, the ones not chosen hidden, so each
                    tab's aria-controls names a panel that is really there. */}
                <div className='vntv-modal-body'>
                    {tabbed ? tabs.map((tab) => (
                        <div
                            key={tab.id}
                            id={`${ids}-panel-${tab.id}`}
                            role='tabpanel'
                            aria-labelledby={`${ids}-tab-${tab.id}`}
                            hidden={tab.id !== active}
                            tabIndex={0}
                            className='vntv-trophy-panel'
                        >{contentOf(tab.id)}</div>
                    )) : contentOf(active)}
                </div>
            </div>
        </div>,
        document.body,
    );
};
