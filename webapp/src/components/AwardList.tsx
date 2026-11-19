import React from 'react';

import {clipperAward} from '../clipLogic';
import {hoursInWords} from '../fans';
import {formatDuration} from '../format';
import type {AwardRow, Boards} from '../types';
import {useBoards} from '../useBoards';

import {AchievementBadge, AllTimeBadge, ClipperBadge, FanBadge, MonthBadge, SnooperBadge, glyphOf} from './badges';
import {MedalIcon} from './icons';

type Props = {
    userId?: string;
};

// Somebody's awards, spelled out: airtime, the Top Snooper and the Best
// Clipper.
//
// The badges alone are a rebus: a crown with a 2 in it means "second for
// airtime, all time" only to somebody who already knows, and the tooltip that
// said so needed a mouse and a guess that there was anything to hover. On a
// profile there is room to simply say it, so it says it.
//
// The icon stays. It is the thing that carries the place at a glance in a post
// header, where there is no room for words, and the two should look like the
// same award in both places.
//
// Which rows, in which order and under what title is the server's to say
// (awards_decide.go rows): the places somebody holds on the boards, the
// titles with one holder, their achievements as one row, and last the months
// already over, since what somebody holds now outranks what they won in the
// spring. They come with the boards (boards.ts), each with its facts; here
// they are given their artwork and their figures are written the reader's
// way.
//
// Renders nothing for the overwhelming majority of people, which is the point
// of an award.

// What a place is called, for the tally of second and third places. Only the
// podium earns one, so there is no fourth case to write.
const TITLES: Record<number, string> = {
    1: 'Champion',
    2: 'Runner-up',
    3: 'Third place',
};

// Somebody's record, as the boards in hand have it: nothing for most people,
// and nothing until the boards have been read.
export function recordOf(boards: Boards, userID?: string): AwardRow[] {
    return (userID && boards.people[userID]?.rows) || [];
}

// The artwork of a place on a board: the crown, the calendar, or the Top
// Snooper's binoculars, which carry no place.
function boardIcon(row: AwardRow): React.ReactNode {
    switch (row.id) {
    case 'alltime':
        return (
            <AllTimeBadge
                rank={row.rank}
                size={22}
            />
        );
    case 'month':
        return (
            <MonthBadge
                rank={row.rank}
                size={22}
            />
        );
    default:
        return <SnooperBadge size={22}/>;
    }
}

// ---- Months, in words ----

// "YYYY-MM", newest first, each once. The server sends them oldest first and
// already without repeats; sorting a handful of strings again costs nothing and
// means this does not depend on it.
export function newestFirst(months: string[] | undefined): string[] {
    return Array.from(new Set(months || [])).sort().reverse();
}

let monthFormat: Intl.DateTimeFormat | null = null;

// "2026-08" as "Aug 2026", in the reader's language. In UTC, because the months
// are UTC months: read in the reader's own zone, midnight on the first of August
// is still July for everybody west of Greenwich. Anything that is not a month
// is shown as it came rather than as a wrong one.
export function formatMonth(month: string): string {
    const match = /^(\d{4})-(0[1-9]|1[0-2])$/.exec(month);
    if (!match) {
        return month;
    }

    if (!monthFormat) {
        monthFormat = new Intl.DateTimeFormat(undefined, {month: 'short', year: 'numeric', timeZone: 'UTC'});
    }

    return monthFormat.format(new Date(Date.UTC(Number(match[1]), Number(match[2]) - 1, 1)));
}

// How many of them are written out before the rest become a count. Three fit
// on a line of the popover; a long run of wins is a row, not a paragraph.
export const MONTHS_SHOWN = 3;

// What the row shows: "Aug 2026, Jul 2026, Jun 2026 +2 more". A month is never
// broken across two lines, so its own spaces are made non-breaking; the line
// wraps between months instead.
export function shortMonthList(months: string[]): string {
    const shown = months.
        slice(0, MONTHS_SHOWN).
        map((month) => formatMonth(month).replace(/ /g, ' ')).
        join(', ');
    const more = months.length - MONTHS_SHOWN;

    return more > 0 ? `${shown} +${more} more` : shown;
}

// Every one of them, for a tooltip and a screen reader, where nothing is cut.
export function fullMonthList(months: string[]): string {
    return months.map(formatMonth).join(', ');
}

export function countMonths(count: number): string {
    return `${count} ${count === 1 ? 'month' : 'months'}`;
}

// "Runner-up in 2 months: Jun 2026, Mar 2026".
export function describeTally(rank: number, months: string[]): string {
    return `${TITLES[rank]} in ${countMonths(months.length)}: ${fullMonthList(months)}`;
}

export const AwardList: React.FC<Props> = ({userId}) => {
    const rows = recordOf(useBoards(), userId);

    if (rows.length === 0) {
        return null;
    }

    return (
        <ul className='vntv-awardlist'>
            {rows.map((row) => {
                switch (row.kind) {
                case 'months':
                    return (
                        <MonthsRow
                            key={row.id}
                            row={row}
                        />
                    );
                case 'clipper':
                    return (
                        <FactRow
                            key={row.id}
                            rank={row.rank}
                            icon={<ClipperBadge size={22}/>}
                            title={row.title}
                            meta={clipperAward(row.upvotes || 0, row.clips || 0)}
                        />
                    );
                case 'fan':
                    return (
                        <FactRow
                            key={row.id}
                            rank={row.rank}
                            icon={<FanBadge size={22}/>}
                            title={row.title}
                            meta={`${hoursInWords(row.hours || 0)} watching`}
                        />
                    );
                case 'achievements':
                    return (
                        <AchievementsRow
                            key={row.id}
                            row={row}
                        />
                    );
                default:

                    // The hours, and what they were spent on when it was not
                    // streaming, said after them: a bare "412h" reads as
                    // airtime, which is what every other row's hours are.
                    return (
                        <FactRow
                            key={row.id}
                            rank={row.rank}
                            icon={boardIcon(row)}
                            title={row.title}
                            meta={`${row.board || ''} · ${formatDuration(row.seconds || 0)}${row.doing ? ` ${row.doing}` : ''}`}
                        />
                    );
                }
            })}
        </ul>
    );
};

// One award as a row: its badge, what it is called, and what it was for.
const FactRow: React.FC<{rank: number; icon: React.ReactNode; title: string; meta: string}> = ({rank, icon, title, meta}) => (
    <li
        className='vntv-factrow'
        data-rank={rank}
    >
        {/* The badge already carries the place as a digit, so the words next
            to it are not read out twice. The binoculars and the clapperboard
            carry none, and "Top Snooper" and "Best Clipper" say it. */}
        <span
            className='vntv-factrow-mark'
            aria-hidden={true}
        >{icon}</span>

        <span className='vntv-factrow-text'>
            {/* No ordinal beside it. The artwork already carries the digit
                and "Runner-up" already means second, so a "2nd" here was the
                same fact for the third time. */}
            <span className='vntv-factrow-title'>{title}</span>
            <span className='vntv-factrow-meta'>{meta}</span>
        </span>
    </li>
);

// The months already over, as one row: the same calendar as the month in
// progress, a count beside the title once there is more than one win, which
// months they were, and second and third places as a small badge and a count
// each.
//
// Built from the same parts as the rows above it so it lines up with them. Only
// the line under the title is different: it wraps rather than cutting off,
// because the months are the point of the row and an ellipsis would hide them.
const MonthsRow: React.FC<{row: AwardRow}> = ({row}) => {
    const gold = row.gold || [];
    const tallies = row.tallies || [];
    const wins = gold.length;

    return (
        <li
            className='vntv-factrow'
            data-rank={row.rank}
        >
            <span
                className='vntv-factrow-mark'
                aria-hidden={true}
            >
                <MonthBadge
                    rank={row.rank}
                    size={22}
                />
            </span>

            <span className='vntv-factrow-text'>
                <span className='vntv-factrow-head'>
                    <span className='vntv-factrow-title'>{row.title}</span>

                    {/* Only from two. "×1" says nothing the title does not;
                        the month itself is on the line underneath. Hidden from
                        a screen reader, which is told the count in words. */}
                    {wins >= 2 ? (
                        <span
                            className='vntv-factrow-count'
                            aria-hidden={true}
                        >{`×${wins}`}</span>
                    ) : null}
                </span>

                <span className='vntv-factrow-meta vntv-factrow-tallies'>
                    {wins > 0 ? (
                        <>
                            {/* Read out as "Monthly champion, 2 months: Aug
                                2026, Jul 2026", every month and no "+2 more";
                                the shortened line is for the eye. */}
                            <span className='vntv-sr-only'>{`, ${countMonths(wins)}: ${fullMonthList(gold)}`}</span>
                            <span
                                className='vntv-factrow-months'
                                aria-hidden={true}
                                title={wins > MONTHS_SHOWN ? fullMonthList(gold) : undefined}
                            >{shortMonthList(gold)}</span>
                        </>
                    ) : null}

                    {tallies.map((tally) => (
                        <span
                            key={tally.rank}
                            className='vntv-factrow-tally'
                            role='img'
                            aria-label={describeTally(tally.rank, tally.months)}
                            title={describeTally(tally.rank, tally.months)}
                        >
                            <MonthBadge
                                rank={tally.rank}
                                size={14}
                            />
                            {`×${tally.months.length}`}
                        </span>
                    ))}
                </span>
            </span>
        </li>
    );
};

// The achievements as one row: "Achievements", then each family's coin and
// its step's name, wrapping rather than cutting off, since the steps are the
// point of the row. The mark is a medal in the row's red.
const AchievementsRow: React.FC<{row: AwardRow}> = ({row}) => (
    <li className='vntv-factrow vntv-factrow-ach'>
        <span
            className='vntv-factrow-mark'
            aria-hidden={true}
        ><MedalIcon size={17}/></span>
        <span className='vntv-factrow-text'>
            <span className='vntv-factrow-title'>{row.title}</span>
            <span className='vntv-factrow-steps'>
                {(row.steps || []).map((step) => (
                    <span
                        key={step.id}
                        className='vntv-factrow-step'
                    >
                        <AchievementBadge
                            kind={glyphOf(step.id)}
                            tier={step.tier}
                            size={22}
                        />
                        {step.name}
                    </span>
                ))}
            </span>
        </span>
    </li>
);
