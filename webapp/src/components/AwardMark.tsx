import React from 'react';

import {CLIPPER_HINT, CLIPPER_LABEL} from '../clipLogic';
import {SNOOPER_LABEL} from '../snooper';
import type {BesideAward} from '../types';

import {AchievementBadge, AllTimeBadge, ClipperBadge, FanBadge, MonthBadge, PastMonthBadge, SnooperBadge, glyphOf} from './badges';

// One award's artwork, whichever it is: what a post header, a Live now card
// and the You tab's chips all draw, so the same award looks the same in each.
// A past month's medal beside a name is the calendar with a star where its
// digit was (Victor's P4 pick), so it is never read as this month's place.
export const AwardIcon: React.FC<{award: BesideAward; size: number}> = ({award, size}) => {
    switch (award.kind) {
    case 'alltime':
        return (
            <AllTimeBadge
                rank={award.rank || 0}
                size={size}
            />
        );
    case 'month':
        return (
            <MonthBadge
                rank={award.rank || 0}
                size={size}
            />
        );
    case 'snooper':
        return <SnooperBadge size={size}/>;
    case 'clipper':
        return <ClipperBadge size={size}/>;
    case 'past':
        return (
            <PastMonthBadge
                rank={award.rank || 0}
                size={size}
            />
        );
    case 'fan':
        return <FanBadge size={size}/>;
    default:
        return (
            <AchievementBadge
                kind={glyphOf(award.family || '')}
                tier={award.tier || 1}
                size={size}
            />
        );
    }
};

// What an award says to a screen reader and when pointed at, in the words
// 3.1.0 used for the four it had, so Automatic is the same down to its
// tooltips.
export function awardWords(award: BesideAward): {label: string; title: string} {
    switch (award.kind) {
    case 'snooper':
        return {label: SNOOPER_LABEL, title: SNOOPER_LABEL};
    case 'clipper':
        return {label: CLIPPER_LABEL, title: CLIPPER_HINT};
    default:
        return {label: award.label, title: award.label};
    }
}

// An award beside a name: its artwork, named.
export const AwardBeside: React.FC<{award: BesideAward; size: number; className?: string}> = ({award, size, className}) => {
    const words = awardWords(award);

    return (
        <span
            className={className}
            role='img'
            aria-label={words.label}
            title={words.title}
        >
            <AwardIcon
                award={award}
                size={size}
            />
        </span>
    );
};
