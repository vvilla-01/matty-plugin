import React from 'react';

import {stepName} from '../awardLogic';
import {askAwardsMine, keptAwardsMine} from '../awardsMine';
import {fetchMyAchievements} from '../client';
import {askOwn} from '../ownAnswers';
import type {AchievementKind, AchievementProgress, AwardsMine} from '../types';
import {useBoards} from '../useBoards';

import {AwardPicker} from './AwardPicker';
import {AchievementBadge, glyphOf} from './badges';

// The Trophies card's You tab (design/3.2.0.md 3.6, Victor's P2 pick), for
// linked readers: what shows by your name, and how far you are along each
// achievement. Everything in it is the reader's own; nothing here is about
// anybody else. Until 3.2.1 it also held a switch for not being named as
// somebody's biggest fan; there is no opt-out any more (design/3.2.1.md 2).
//
// Mounted only while the tab is the one showing, so what it asks for (GET
// /awards/mine, GET /achievements/mine) is asked when it opens, and both
// answers go at sign-out with the card; one still on its way then is answered
// to nobody (ownAnswers.ts, 2.6). The last answer about awards is also kept
// for the reader (awardsMine.ts), so the picker is drawn from it at once.
export const TrophiesYou: React.FC<{userId: string; name: string}> = ({userId, name}) => {
    const boards = useBoards();
    const kinds = boards.achievementKinds || [];
    const [mine, setMine] = React.useState<AwardsMine | null>(null);
    const [loadFailed, setLoadFailed] = React.useState(false);
    const [loadKey, setLoadKey] = React.useState(0);

    React.useEffect(() => {
        let current = true;
        setLoadFailed(false);
        askAwardsMine(userId, boards.version).then((answer) => {
            if (current && answer) {
                setMine(answer);
            }
        }).catch(() => {
            if (current) {
                setLoadFailed(true);
            }
        });

        return () => {
            current = false;
        };
    }, [loadKey]);

    // What the reader holds is part of that answer, and it changes when the
    // boards do (a place won or lost, a new step): asked again then, for the
    // held awards alone, so a choice being made is not disturbed. Quietly: a
    // read that fails leaves the picker as it was.
    const loaded = mine !== null;
    React.useEffect(() => {
        if (!loaded) {
            return undefined;
        }

        let current = true;
        askAwardsMine(userId, boards.version, true).then((answer) => {
            if (current && answer) {
                setMine((held) => (held ? {...held, held: answer.held} : held));
            }
        }).catch(() => {
            // The picker keeps what it has.
        });

        return () => {
            current = false;
        };
    }, [boards.version]); // eslint-disable-line react-hooks/exhaustive-deps

    return (
        <div className='vntv-you'>
            <section className='vntv-you-section'>
                <h3 className='vntv-lb-board-heading'>{'By your name'}</h3>
                <AwardPicker
                    userId={userId}
                    name={name}
                    mine={mine}
                    kept={keptAwardsMine(userId, boards.version)}
                    loadFailed={loadFailed}
                    onRetry={() => setLoadKey((key) => key + 1)}
                    onSaved={(show) => setMine((current) => (current ? {...current, show} : current))}
                />
            </section>

            {kinds.length > 0 ? (
                <section className='vntv-you-section'>
                    <h3 className='vntv-lb-board-heading'>{'Your achievements'}</h3>
                    <MyProgress
                        kinds={kinds}
                        held={(boards.achievements || []).find((entry) => entry.userId === userId)?.have || []}
                    />
                </section>
            ) : null}
        </div>
    );
};

// Each family with its coin, a thin bar to the next step and how far along:
// "62 of 100 hours". The bar is filled to the same share the words give, from
// nothing to the next step, so the two never disagree; a bar for only the
// stretch since the last step read as a fault under "262 of 500 hours".
//
// Until the figures arrive, or if they cannot be had, each family shows the
// step the boards say the reader holds, with no bar and no figure: no bars
// rather than wrong ones, never "0 of 10 hours" for somebody at 250.
const MyProgress: React.FC<{kinds: AchievementKind[]; held: Array<{id: string; tier: number}>}> = ({kinds, held}) => {
    const [progress, setProgress] = React.useState<AchievementProgress | null>(null);
    const [failed, setFailed] = React.useState(false);
    const [asking, setAsking] = React.useState(0);

    React.useEffect(() => {
        let current = true;
        setFailed(false);
        askOwn(fetchMyAchievements).then((answer) => {
            if (current && answer) {
                setProgress(answer);
            }
        }).catch(() => {
            if (current) {
                setFailed(true);
            }
        });

        return () => {
            current = false;
        };
    }, [asking]);

    return (
        <>
            <ul
                className='vntv-you-progress'
                data-loading={progress ? undefined : 'true'}
            >
                {kinds.map((kind) => {
                    const mine = progress?.progress.find((entry) => entry.id === kind.id);
                    const known = Boolean(progress);
                    const value = mine ? mine.value : 0;
                    const tier = known ? kind.tiers.filter((step) => value >= step).length : (held.find((entry) => entry.id === kind.id)?.tier || 0);
                    const next = mine ? mine.next : kind.tiers[0];
                    const share = next > 0 ? Math.max(0, Math.min(1, value / next)) : 1;
                    const unit = unitOf(kind, next || value);
                    // A space while the figure is not known, so the line
                    // keeps its height and nothing moves when it comes.
                    let meta = '\u00a0';
                    if (known) {
                        meta = next > 0 ? `${value} of ${next} ${unit}` : 'All done.';
                    }

                    return (
                        <li
                            key={kind.id}
                            className='vntv-you-step'
                            data-none={tier === 0 ? 'true' : undefined}
                        >
                            <span
                                className='vntv-you-coin'
                                aria-hidden={true}
                            >
                                <AchievementBadge
                                    kind={glyphOf(kind.id)}
                                    tier={Math.max(1, tier)}
                                    size={26}
                                />
                            </span>
                            <span className='vntv-you-words'>
                                <span className='vntv-you-name'>{tier > 0 ? stepName(kind, tier) : `Next: ${stepName(kind, 1)}`}</span>
                                <span
                                    className='vntv-you-bar'
                                    aria-hidden={true}
                                >
                                    {known ? (
                                        <span
                                            className='vntv-you-fill'
                                            style={{width: `${Math.round(share * 100)}%`}}
                                        />
                                    ) : null}
                                </span>
                                <span className='vntv-you-meta'>{meta}</span>
                            </span>
                        </li>
                    );
                })}
            </ul>
            {failed && !progress ? (
                <div className='vntv-awardpick-failed'>
                    <span>{'Couldn’t load how far along you are just now.'}</span>
                    <button
                        type='button'
                        className='vntv-sheet-action'
                        onClick={() => setAsking((count) => count + 1)}
                    >{'Try again'}</button>
                </div>
            ) : null}
        </>
    );
};

function unitOf(kind: AchievementKind, count: number): string {
    switch (kind.unit) {
    case 'months':
        return count === 1 ? 'month' : 'months';
    case 'thumbs':
        return 'thumbs up';
    default:
        return count === 1 ? 'hour' : 'hours';
    }
}
