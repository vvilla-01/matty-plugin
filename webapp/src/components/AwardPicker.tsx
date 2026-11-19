import React from 'react';

import {AUTOMATIC, MAX_SHOWN, NOT_HELD, besideName, nameOfId, newAwards, pickerFrom, shownAwards} from '../awardLogic';
import type {HeldAward} from '../awardLogic';
import {RequestError, saveAwardChoice} from '../client';
import {ownGeneration} from '../ownAnswers';
import {useSnapshot} from '../store';
import type {AwardsMine} from '../types';
import {useBoards} from '../useBoards';
import {voiceOf} from '../voice';

import {AwardIcon} from './AwardMark';
import {Avatar} from './Avatar';
import {VoiceBadge} from './badges';
import {ChevronIcon} from './icons';

// The award picker (design/3.2.0.md 3.6), in the Trophies card's You tab
// (Victor's P2 pick): what shows beside your name, chosen by pressing the
// awards you hold.
//
// Top to bottom: a post header as it will look, updated as anything changes;
// Automatic or My choice; then every award you hold as a chip, the chosen ones
// numbered in their order. Saved by itself 800 ms after the last change.
//
// What the server holds (GET /awards/mine) is the You tab's, which asks once
// and shares it with the biggest fan switch.

type Props = {
    userId: string;
    name: string;

    // The reader's own choice as the server holds it; null while it loads.
    mine: AwardsMine | null;

    // The last answer this page had for the reader (awardsMine.ts), drawn
    // while this opening's is on its way or failed; null for none.
    kept: AwardsMine | null;
    loadFailed: boolean;
    onRetry: () => void;
    onSaved: (show: string[] | null) => void;
};

// The awards somebody held when they last opened the picker in this browser,
// for NEW (an approved departure: kept per browser, and the first opening is
// silent). Only a convenience: storage that does not work means no NEW marks.
const SEEN_KEY = 'villanet.awardsSeen.';

function readSeen(userId: string): string[] | null {
    try {
        const raw = window.localStorage.getItem(SEEN_KEY + userId);

        return raw ? JSON.parse(raw) as string[] : null;
    } catch {
        return null;
    }
}

function writeSeen(userId: string, ids: string[]): void {
    try {
        window.localStorage.setItem(SEEN_KEY + userId, JSON.stringify(ids));
    } catch {
        // Not kept: the next opening marks nothing as new.
    }
}

const SAVE_AFTER_MS = 800;

type Status = '' | 'saving' | 'saved' | 'failed';

export const AwardPicker: React.FC<Props> = ({userId, name, mine, kept, loadFailed, onRetry, onSaved}) => {
    const snapshot = useSnapshot();
    const boards = useBoards();
    const [draft, setDraft] = React.useState<string[] | null>(null);
    const [status, setStatus] = React.useState<Status>('');

    // The choice a failed save was for, so Try again sends it again.
    const [failedChoice, setFailedChoice] = React.useState<string[] | null>(null);
    const timer = React.useRef<number | null>(null);
    const chipRefs = React.useRef<Record<string, HTMLButtonElement | null>>({});

    // Everything the reader holds, as the server says with their choice:
    // which awards those are is its to decide (awards_decide.go), and the
    // You tab asks again when the boards change, so a place won or lost
    // while the picker is open shows here as it does beside their name.
    // While this opening's answer is on its way, the last one (pickerFrom).
    const beside = besideName(snapshot, userId);
    const {held, shown, known} = pickerFrom(mine, kept, draft, beside);

    // What the server holds, once it has said: the chips start from it. And
    // NEW: what was not held the last time, read once per opening and then
    // brought up to date, so the marks last until the picker is next opened.
    const [fresh, setFresh] = React.useState<string[]>([]);
    const loaded = mine !== null;
    React.useEffect(() => {
        if (mine) {
            setDraft(mine.show);
            setFresh(newAwards(mine.held, readSeen(userId), null));
            writeSeen(userId, mine.held.map((award) => award.id));
        }
    }, [loaded]); // eslint-disable-line react-hooks/exhaustive-deps

    // A change still waiting for its 800 ms when the picker goes (the card
    // closed, or another tab chosen) is sent at once rather than dropped:
    // the reader was told "Saving…". Not after a sign-out, when it is no
    // longer theirs to send.
    const pending = React.useRef<{next: string[] | null; generation: number} | null>(null);
    const onSavedRef = React.useRef(onSaved);
    onSavedRef.current = onSaved;
    React.useEffect(() => () => {
        if (timer.current !== null) {
            window.clearTimeout(timer.current);
            timer.current = null;
        }
        const waiting = pending.current;
        pending.current = null;
        if (waiting && waiting.generation === ownGeneration()) {
            saveAwardChoice(waiting.next).then((answer) => onSavedRef.current(storedOf(answer, waiting.next))).catch(() => {
                // Nobody left to tell; the tab shows what the server holds
                // when it next opens.
            });
        }
    }, []);

    const save = (next: string[] | null) => {
        setDraft(next);
        setStatus('saving');
        if (timer.current !== null) {
            window.clearTimeout(timer.current);
        }
        pending.current = {next, generation: ownGeneration()};
        timer.current = window.setTimeout(() => {
            timer.current = null;
            pending.current = null;
            saveAwardChoice(next).then((answer) => {
                // What the server stored, which can be less than was sent: a
                // 24/7 channel family that has left the catalogue comes out
                // (its dimmed chip with it), unless the reader has changed
                // something again meanwhile.
                const stored = storedOf(answer, next);
                setStatus('saved');
                onSaved(stored);
                if (timer.current === null && !sameChoice(stored, next)) {
                    setDraft(stored);
                }
            }).catch((reason) => {
                // A refusal for access has already had "me" read again, which
                // takes the tab away; anything else is said, and the chips go
                // back to what the server holds.
                if (!(reason instanceof RequestError && (reason.code === 'watch_only' || reason.code === 'locked'))) {
                    setStatus('failed');
                    setFailedChoice(next);
                    setDraft(mine ? mine.show : null);
                }
            });
        }, SAVE_AFTER_MS);
    };

    const loading = mine === null;
    const automatic = draft === null;
    const chosen = draft || [];
    const full = chosen.length >= MAX_SHOWN;

    const segmentRefs = React.useRef<Array<HTMLButtonElement | null>>([]);
    const chooseAutomatic = () => {
        if (!loading && !automatic) {
            save(null);
        }
    };

    // From Automatic, My choice starts from what Automatic shows, so the
    // first press changes nothing anybody sees.
    const chooseMine = () => {
        if (!loading && automatic) {
            save(shownAwards(held, null).map((award) => award.id));
        }
    };

    const toggle = (id: string) => {
        if (automatic || loading) {
            return;
        }
        if (chosen.includes(id)) {
            save(chosen.filter((other) => other !== id));
        } else if (!full) {
            save([...chosen, id]);
        }
    };

    const move = (id: string, by: number) => {
        const from = chosen.indexOf(id);
        const to = from + by;
        if (from < 0 || to < 0 || to >= chosen.length) {
            return;
        }
        const next = chosen.slice();
        next.splice(from, 1);
        next.splice(to, 0, id);
        save(next);
        window.requestAnimationFrame(() => chipRefs.current[id]?.focus());
    };

    // Every award held, then any chosen that is not held now, kept in the
    // choice and shown dimmed where it was put.
    const lost = chosen.filter((id) => !held.some((award) => award.id === id));
    const chips: Array<{id: string; award?: HeldAward; ghost?: HeldAward; label: string}> = [
        ...held.map((award) => ({id: award.id, award, label: award.short})),
        ...lost.map((id) => ({id, ghost: ghostOf(id), label: nameOfId(id, boards.achievementKinds || [], boards.fans)})),
    ];

    const voice = voiceOf(snapshot, userId);

    let hint = 'Up to four, in this order. Press one to add it or take it out.';
    if (held.length === 0 && automatic && (known || beside.length === 0)) {
        hint = 'Nothing to show yet. Awards and achievements you earn will be here to choose from.';
    } else if (automatic) {
        hint = 'Shows your places on the boards, Top Snooper and Best Clipper whenever you hold them.';
    } else if (chosen.length === 0) {
        hint = 'Nothing beside your name. The headphones still show while you’re on Mumble.';
    } else if (full) {
        hint = 'Four is the most. Take one out to add another.';
    }

    let said = '';
    if (status === 'saving') {
        said = 'Saving…';
    } else if (status === 'saved') {
        said = 'Saved. Everybody sees it within a few seconds.';
    }

    return (
        <div className='vntv-awardpick'>
            {/* A post as it will look: the face, the name, the headphones while
                on Mumble, the badges, the time. Hidden from a screen reader:
                the chips below say the same in words. */}
            <div
                className='vntv-awardpick-preview'
                aria-hidden={true}
            >
                <Avatar
                    userId={userId}
                    name={name}
                    className='vntv-awardpick-face'
                    decorative={true}
                />
                <span className='vntv-awardpick-post'>
                    <span className='vntv-awardpick-head'>
                        <span className='vntv-awardpick-name'>{name}</span>
                        <span className='vntv-awardpick-badges'>
                            {voice ? (
                                <VoiceBadge
                                    muted={voice.muted}
                                    size={16}
                                />
                            ) : null}
                            {shown.map((award) => (
                                <AwardIcon
                                    key={award.id}
                                    award={award}
                                    size={16}
                                />
                            ))}
                        </span>
                        <span className='vntv-awardpick-time'>{'9:41 PM'}</span>
                    </span>
                    <span className='vntv-awardpick-message'>{'Going live in ten.'}</span>
                </span>
            </div>

            {mine && !mine.readable ? (
                <p className='vntv-awardpick-note'>{'Your choices could not be read just now, so your awards show automatically.'}</p>
            ) : null}

            {/* A radio group as ARIA has it: one tab stop, the checked
                segment, and the arrow keys move to the other and choose it. */}
            <div
                className='vntv-segments'
                role='radiogroup'
                aria-label='What shows by your name'
                onKeyDown={(event) => {
                    if (!['ArrowLeft', 'ArrowRight', 'ArrowUp', 'ArrowDown'].includes(event.key)) {
                        return;
                    }
                    event.preventDefault();
                    if (loading) {
                        return;
                    }
                    if (automatic) {
                        chooseMine();
                    } else {
                        chooseAutomatic();
                    }
                    segmentRefs.current[automatic ? 1 : 0]?.focus();
                }}
            >
                <button
                    ref={(element) => {
                        segmentRefs.current[0] = element;
                    }}
                    type='button'
                    role='radio'
                    className='vntv-segment'
                    tabIndex={automatic ? 0 : -1}
                    aria-checked={automatic}
                    aria-disabled={loading ? true : undefined}
                    onClick={chooseAutomatic}
                >{'Automatic'}</button>
                <button
                    ref={(element) => {
                        segmentRefs.current[1] = element;
                    }}
                    type='button'
                    role='radio'
                    className='vntv-segment'
                    tabIndex={automatic ? -1 : 0}
                    aria-checked={!automatic}
                    aria-disabled={loading ? true : undefined}
                    onClick={chooseMine}
                >{'My choice'}</button>
            </div>

            {loadFailed ? (
                <div className='vntv-awardpick-failed'>
                    <span>{'Couldn’t load your choices just now.'}</span>
                    <button
                        type='button'
                        className='vntv-sheet-action'
                        onClick={onRetry}
                    >{'Try again'}</button>
                </div>
            ) : (
                <p className='vntv-awardpick-hint'>{hint}</p>
            )}

            <ul
                className='vntv-awardpick-chips'
                data-loading={loading ? 'true' : undefined}
            >
                {chips.map((chip) => {
                    const at = chosen.indexOf(chip.id);
                    const picked = !automatic && at >= 0;
                    const autoShown = automatic && AUTOMATIC.includes(chip.id) && Boolean(chip.award);
                    const blocked = !automatic && !picked && full;
                    const isNew = fresh.includes(chip.id) && !picked;

                    return (
                        <li
                            key={chip.id}
                            className='vntv-awardpick-item'
                            data-picked={picked ? 'true' : undefined}
                        >
                            <button
                                ref={(element) => {
                                    chipRefs.current[chip.id] = element;
                                }}
                                type='button'
                                className='vntv-awardchip'
                                aria-pressed={automatic ? autoShown : picked}
                                aria-disabled={automatic || loading || blocked ? true : undefined}
                                aria-label={`${chip.award ? chip.award.label : chip.label}${chip.award ? '' : `, ${NOT_HELD}`}${picked ? `, ${at + 1} of ${chosen.length}` : ''}`}
                                data-held={chip.award ? 'true' : 'false'}
                                data-auto={automatic ? (autoShown ? 'shown' : 'hidden') : undefined}
                                onClick={() => toggle(chip.id)}
                                onKeyDown={(event) => {
                                    if (!picked || !event.altKey) {
                                        return;
                                    }
                                    if (event.key === 'ArrowLeft' || event.key === 'ArrowRight') {
                                        event.preventDefault();
                                        move(chip.id, event.key === 'ArrowLeft' ? -1 : 1);
                                    }
                                }}
                            >
                                <span
                                    className='vntv-awardchip-art'
                                    aria-hidden={true}
                                >
                                    {chip.award || chip.ghost ? (
                                        <AwardIcon
                                            award={(chip.award || chip.ghost) as HeldAward}
                                            size={22}
                                        />
                                    ) : <span className='vntv-awardchip-gone'/>}
                                </span>
                                <span className='vntv-awardchip-words'>
                                    <span className='vntv-awardchip-label'>{chip.label}</span>
                                    {chip.award ? null : <span className='vntv-awardchip-sub'>{NOT_HELD}</span>}
                                </span>
                                {picked ? (
                                    <span
                                        className='vntv-awardchip-place'
                                        aria-hidden={true}
                                    >{at + 1}</span>
                                ) : null}
                                {isNew ? (
                                    <span
                                        className='vntv-awardchip-new'
                                        aria-hidden={true}
                                    >{'New'}</span>
                                ) : null}
                            </button>
                            {picked && chosen.length > 1 ? (
                                <span className='vntv-awardchip-moves'>
                                    <button
                                        type='button'
                                        className='vntv-awardchip-move'
                                        data-way='earlier'
                                        aria-label='Move earlier'
                                        title='Move earlier (Alt+←)'
                                        disabled={at === 0}
                                        onClick={() => move(chip.id, -1)}
                                    ><ChevronIcon size={11}/></button>
                                    <button
                                        type='button'
                                        className='vntv-awardchip-move'
                                        aria-label='Move later'
                                        title='Move later (Alt+→)'
                                        disabled={at === chosen.length - 1}
                                        onClick={() => move(chip.id, 1)}
                                    ><ChevronIcon size={11}/></button>
                                </span>
                            ) : null}
                        </li>
                    );
                })}
            </ul>

            {status === 'failed' ? (
                <div
                    className='vntv-awardpick-status vntv-awardpick-failed'
                    role='alert'
                >
                    <span>{'Couldn’t save that. Try again.'}</span>
                    <button
                        type='button'
                        className='vntv-sheet-action'
                        onClick={() => save(failedChoice)}
                    >{'Try again'}</button>
                </div>
            ) : (
                <p
                    className='vntv-awardpick-status'
                    role='status'
                >{said}</p>
            )}
        </div>
    );
};

// The choice the server says it stored, or what was sent when it says
// nothing of it.
function storedOf(answer: {show?: string[] | null} | null | undefined, sent: string[] | null): string[] | null {
    return answer && answer.show !== undefined ? answer.show : sent;
}

function sameChoice(a: string[] | null, b: string[] | null): boolean {
    if (a === null || b === null) {
        return a === b;
    }

    return a.length === b.length && a.every((id, at) => id === b[at]);
}

// What an award not held now looks like on its dimmed chip: its own artwork
// at its first place or step, so it is still recognisably the one chosen.
function ghostOf(id: string): HeldAward | undefined {
    if (id.startsWith('fan:')) {
        return {id, kind: 'fan', label: '', short: ''};
    }
    if (id.startsWith('ach:')) {
        return {id, kind: 'ach', family: id.slice(4), tier: 1, label: '', short: ''};
    }

    switch (id) {
    case 'alltime':
    case 'month':
    case 'past':
    case 'snooper':
    case 'clipper':
        return {id, kind: id, rank: 1, label: '', short: ''};
    default:
        return undefined;
    }
}
