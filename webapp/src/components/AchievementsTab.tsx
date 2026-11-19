import React from 'react';

import {familyCounts, familyHead, stepName, stepShort} from '../awardLogic';
import {displayNameOf, profileOf} from '../mattermost';
import type {AchievementHolder, AchievementKind, Boards} from '../types';

import {Avatar} from './Avatar';
import {AchievementBadge, glyphOf} from './badges';

// The Trophies card's Achievements tab (design/3.2.0.md 5.7), for everybody
// who may open the card, by family (Victor's P4 pick): each family's ladder
// of steps, highest first, with the people standing on each. A step nobody
// has reached yet stays, quieter, so the ladder says how far it goes. The
// reader's own rows are lit, as on the boards; the 24/7 channels' families say
// they count hours watching.

const EMPTY = 'Nobody has an achievement yet. The first comes at 10 hours streamed or watched.';

// Somebody's name as the server named them with the boards (an approved
// departure from 5.3: the holders carry their names), else from Mattermost's
// own store.
function nameOf(holder: AchievementHolder): string {
    if (holder.displayName) {
        return holder.displayName;
    }

    const user = profileOf(holder.userId);
    if (user) {
        return displayNameOf(user);
    }

    return holder.username ? `@${holder.username}` : 'Somebody';
}

function tierIn(holder: AchievementHolder, kindId: string): number {
    return holder.have.find((entry) => entry.id === kindId)?.tier || 0;
}

export const AchievementsTab: React.FC<{boards: Boards; meUserId?: string}> = ({boards, meUserId}) => {
    const kinds = boards.achievementKinds || [];
    const holders = boards.achievements || [];

    if (kinds.length === 0 || holders.length === 0) {
        return <p className='vntv-lb-empty'>{EMPTY}</p>;
    }

    return (
        <div className='vntv-ach-families'>
            {kinds.map((kind) => (
                <Family
                    key={kind.id}
                    kind={kind}
                    holders={holders}
                    meUserId={meUserId}
                />
            ))}
        </div>
    );
};

// One family as a ladder: its coin at the highest step anybody holds, its
// head and what it counts, then every step from the top down with the people
// on it.
const Family: React.FC<{kind: AchievementKind; holders: AchievementHolder[]; meUserId?: string}> = ({kind, holders, meUserId}) => {
    const top = holders.reduce((most, holder) => Math.max(most, tierIn(holder, kind.id)), 0);
    const count = holders.filter((holder) => tierIn(holder, kind.id) > 0).length;
    const steps = kind.tiers.map((step, index) => ({step, tier: index + 1})).reverse();

    return (
        <section className='vntv-ach-family'>
            <header className='vntv-ach-family-head'>
                <span
                    className='vntv-ach-family-coin'
                    aria-hidden={true}
                >
                    <AchievementBadge
                        kind={glyphOf(kind.id)}
                        tier={Math.max(1, top)}
                        size={30}
                    />
                </span>
                <span className='vntv-ach-family-words'>
                    <h3 className='vntv-lb-board-heading'>{familyHead(kind)}</h3>
                    <p className='vntv-lb-board-sub'>{`${familyCounts(kind)} · ${count} ${count === 1 ? 'person' : 'people'}`}</p>
                </span>
            </header>

            <ol className='vntv-ach-ladder'>
                {steps.map(({step, tier}) => {
                    const here = holders.filter((holder) => tierIn(holder, kind.id) === tier);

                    return (
                        <li
                            key={tier}
                            className='vntv-ach-rung'
                            data-empty={here.length === 0 ? 'true' : undefined}
                            data-me={here.some((holder) => holder.userId === meUserId) ? 'true' : undefined}
                        >
                            <span
                                className='vntv-ach-rung-coin'
                                role='img'
                                aria-label={stepName(kind, tier)}
                                title={stepName(kind, tier)}
                            >
                                <AchievementBadge
                                    kind={glyphOf(kind.id)}
                                    tier={tier}
                                    size={18}
                                />
                            </span>
                            <span className='vntv-ach-rung-step'>{stepShort(kind, step)}</span>
                            <span className='vntv-ach-rung-people'>
                                {here.length === 0 ? <span className='vntv-ach-rung-none'>{'—'}</span> : here.map((holder) => {
                                    const name = nameOf(holder);

                                    return (
                                        <span
                                            key={holder.userId}
                                            className='vntv-ach-person'
                                            data-me={holder.userId === meUserId ? 'true' : undefined}
                                        >
                                            <Avatar
                                                userId={holder.userId}
                                                name={name}
                                                className='vntv-ach-face'
                                                decorative={true}
                                            />
                                            {name}
                                        </span>
                                    );
                                })}
                            </span>
                        </li>
                    );
                })}
            </ol>
        </section>
    );
};
