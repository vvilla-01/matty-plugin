import React from 'react';

import {editPlan, planStream} from '../client';
import {useIdentity} from '../identity';
import {composing} from '../keys';
import {formatWhen, viewerZone} from '../time';
import type {MediaResult, ScheduleItem} from '../types';

import {CATEGORIES, CategoryIcon} from './categories';
import {CheckIcon} from './icons';
import {ShareClipButton, ShareClipStatus, useShare} from './ShareClipButton';
import {TitleSearch} from './TitleSearch';
import {WhenPicker} from './WhenPicker';

// Saying when you will stream. What has been said lives in the Up next rail,
// which is only there while something is planned; the way in is the calendar
// in the footer's bar, which is always there, and this opens in a sheet above
// it (Sidebar.tsx). The pencil on a plan of your own opens it there too,
// filled in.
//
// The form is a box for what you are streaming and one button showing when.
// Pressing that opens a calendar; choosing a day turns the calendar into a
// clock. THERE IS NOTHING TO TYPE A TIME INTO.
//
// Two rows, each one joined box. The first is WHAT: the movie and game
// toggles are part of the text box, on its left, so saying what kind of stream
// costs no row of its own. The second is WHEN, with the save button joined to
// its right end — the last thing in the form, where the eye already is when the
// time has been chosen. Enter in the text box saves too.
//
// There is no Cancel. The sheet's own × closes it, so does pressing the
// calendar again, and Escape; a fourth way to do the same thing was a whole
// button's worth of space saying so.
//
// It took three goes to get here. A datetime-local field first, which is the
// technically correct control and a miserable one — the text is parsed as you
// type and the keyboard is the fastest known way to enter a date that is wrong.
// Then three rows of buttons, which removed the typing and replaced it with
// three decisions and a lot of furniture. Then two dropdowns, which were tidy
// and still asked somebody to scroll a list of forty-eight times. A calendar
// and a clock is the thing everybody already has in their pocket.
type Props = {
    // A plan of your own to change, instead of a new one to make.
    plan?: ScheduleItem | null;

    onChanged: () => void;

    // Called when the form is finished with — saved or cancelled — so the
    // footer can slide its sheet shut. Every opening is a fresh form: the panel keys it, so
    // nothing typed the last time survives to the next.
    onDone: () => void;
};

// An hour from now, rounded up to the next half hour: close enough to be
// useful, far enough away to be plausible.
function defaultStart(): number {
    const soon = new Date(Date.now() + 3600000);
    soon.setSeconds(0, 0);
    const minutes = soon.getMinutes();
    soon.setMinutes(minutes === 0 ? 0 : (minutes <= 30 ? 30 : 60));

    return soon.getTime();
}

// The server's own window: never the past, never more than ninety days out.
// Both ends are passed to the picker, which then cannot offer a moment outside
// them — the bound is not a rejection after the fact.
const HORIZON_MS = 90 * 24 * 3600000;

// A minute of slack at the near end, so a moment chosen and saved a few seconds
// later is still in the future when the server looks at it.
const SLACK_MS = 60000;

// How long the sheet stays after a save, offering to share what was saved
// (design/3.2.0.md 6.6), before it slides shut by itself.
const SHARE_OFFER_MS = 20000;

type Saved = {id: string; title: string; startsAt: number; category: string};

// The sheet's face once a new plan is saved: what was saved, "Planned.", and
// Share in the channel that is open, the clip share's button and lines. It
// closes itself after 20 s, unless a share is under way; its × and Escape
// close it sooner, as before.
//
// It takes the place of the form, and with it the button that saved, so the
// focus goes to Share as it opens (or to the face itself), where Escape and
// the keyboard carry on; and "Planned." is said by a live region that is in
// the page before the words are put in it.
const PlanSaved: React.FC<{saved: Saved; onDone: () => void}> = ({saved, onDone}) => {
    const share = useShare({kind: 'plan', id: saved.id});
    const zone = viewerZone();
    const sharing = share.words === 'Sharing…';
    const sharingRef = React.useRef(sharing);
    sharingRef.current = sharing;
    const face = React.useRef<HTMLDivElement | null>(null);
    const [planned, setPlanned] = React.useState('');

    React.useEffect(() => {
        const active = document.activeElement;
        const panel = face.current?.closest('.vntv-sheet');
        if (!active || active === document.body || panel?.contains(active)) {
            (share.button.current || face.current)?.focus({preventScroll: true});
        }
        const said = window.setTimeout(() => setPlanned('Planned.'), 100);

        return () => window.clearTimeout(said);
    }, []); // eslint-disable-line react-hooks/exhaustive-deps

    React.useEffect(() => {
        const timer = window.setTimeout(() => {
            if (!sharingRef.current) {
                onDone();
            }
        }, SHARE_OFFER_MS);

        return () => window.clearTimeout(timer);
    }, [onDone]);

    return (
        <div
            ref={face}
            className='vntv-sched-saved'
            tabIndex={-1}
            onKeyDown={(event) => {
                if (event.key === 'Escape' && !composing(event)) {
                    event.stopPropagation();
                    onDone();
                }
            }}
        >
            <p className='vntv-sched-saved-what'>
                <CategoryIcon
                    category={saved.category}
                    className='vntv-sched-saved-kind'
                    size={14}
                />
                <b>{saved.title}</b>
                <span>{formatWhen(saved.startsAt, zone)}</span>
            </p>
            <div className='vntv-sched-saved-row'>
                <p className='vntv-sched-saved-done'>
                    <CheckIcon size={14}/>
                    {'Planned.'}
                </p>
                <span
                    className='vntv-sr-only'
                    role='status'
                >{planned}</span>
                <ShareClipButton
                    share={share}
                    className='vntv-pill'
                />
            </div>
            <ShareClipStatus share={share}/>
        </div>
    );
};

export const ScheduleControls: React.FC<Props> = ({plan, onChanged, onDone}) => {
    const [saved, setSaved] = React.useState<Saved | null>(null);
    const [picking, setPicking] = React.useState(false);
    const [title, setTitle] = React.useState(() => plan?.title || '');
    const [category, setCategory] = React.useState(() => plan?.category || '');
    const [startsAt, setStartsAt] = React.useState(() => plan?.startsAt || defaultStart());
    const [busy, setBusy] = React.useState(false);
    const [error, setError] = React.useState('');

    // The movie, show or game picked from the suggestions, if one was.
    const [picked, setPicked] = React.useState<MediaResult | null>(() => (plan?.media ? {
        ref: plan.media.ref,
        kind: plan.media.kind,
        title: plan.media.title,
        year: plan.media.year,
    } : null));

    // What the title box looks up: movies and shows for a movie night, games
    // for a game, nothing otherwise — and nothing at all unless the server
    // says it can. Off unless an admin has switched lookups on.
    const me = useIdentity();
    const lookupKind = (() => {
        if (category === 'movie' && me?.media?.movies) {
            return 'movie' as const;
        }
        if (category === 'game' && me?.media?.games) {
            return 'game' as const;
        }

        return null;
    })();

    // A game linked to a plan that has just become a movie night is not that
    // plan's any more. Decided by the kind of plan, not by whether lookups
    // are available: a plan linked before an admin switched them off keeps
    // its link when it is changed.
    React.useEffect(() => {
        if (!picked) {
            return;
        }

        const fits = picked.kind === 'game' ? category === 'game' : category === 'movie';
        if (!fits) {
            setPicked(null);
        }
    }, [category, picked]);

    const zone = viewerZone();

    // A form can sit open for a while. Without this, the window it offers would
    // be the one from whenever it was opened.
    const [now, setNow] = React.useState(() => Date.now());
    React.useEffect(() => {
        const timer = window.setInterval(() => setNow(Date.now()), 30000);

        return () => window.clearInterval(timer);
    }, []);

    const earliest = now + SLACK_MS;
    const latest = now + HORIZON_MS;

    // A plan being changed that has already started keeps the time it
    // started at unless somebody picks a new one: fixing a typo in its title
    // must not quietly move it into the future and off "Live now". The server
    // takes an unchanged time as it is, for the same reason.
    const keepsItsTime = Boolean(plan) && startsAt === plan?.startsAt;

    // Time passing under an open form, rather than the form being wrong about
    // it. Only ever moves forward, and only when it has to.
    React.useEffect(() => {
        if (startsAt < earliest && !keepsItsTime) {
            setStartsAt(earliest + (300000 - (earliest % 300000)));
        }
    }, [startsAt, earliest, keepsItsTime]);

    const ready = title.trim() !== '' && (keepsItsTime || (startsAt >= earliest && startsAt <= latest));

    // Whether this form is still the one on screen. A save still on its way
    // when the form was closed and a fresh one opened must not shut the fresh
    // one when it lands. Set in the effect body, so StrictMode's mount, unmount,
    // mount leaves it true.
    const alive = React.useRef(false);
    React.useEffect(() => {
        alive.current = true;

        return () => {
            alive.current = false;
        };
    }, []);

    const whenRef = React.useRef<HTMLButtonElement | null>(null);

    // However the calendar and clock close — Done, the last turn of the dial,
    // Escape — focus goes back to the button that opened them. Otherwise it
    // goes down with them, out of the panel, and Escape stops reaching the
    // form at all.
    const closePicker = React.useCallback(() => {
        setPicking(false);
        whenRef.current?.focus();
    }, []);

    const save = React.useCallback(() => {
        setBusy(true);
        setError('');

        const saving = plan ?
            editPlan(plan.id, title, startsAt, category, picked?.ref) :
            planStream(title, startsAt, category, picked?.ref);

        saving.then((answer) => {
            onChanged();
            if (!alive.current) {
                return;
            }

            // A new plan, named by the server's answer: offered for sharing
            // before the sheet goes (design/3.2.0.md 6.6). A change, or an
            // older server's answer with no id, closes it as before.
            const id = (answer as {id?: unknown}).id;
            if (!plan && typeof id === 'string' && id !== '') {
                setSaved({id, title: title.trim(), startsAt, category});

                return;
            }

            onDone();
        }).catch((reason: unknown) => {
            setError(reason instanceof Error ? reason.message : String(reason));
        }).finally(() => setBusy(false));
    }, [plan, title, startsAt, category, picked, onChanged, onDone]);

    const saveLabel = (() => {
        if (busy) {
            return 'Saving…';
        }

        if (title.trim() === '') {
            return 'Say what you are streaming first';
        }

        return plan ? 'Save the changes' : 'Add to Up next';
    })();

    if (saved) {
        return (
            <PlanSaved
                saved={saved}
                onDone={onDone}
            />
        );
    }

    return (

        // Escape from anywhere in the form, not just the text box — with no
        // Cancel button it is one of the two ways out. From inside the
        // calendar or clock it closes that first, and puts focus back on the
        // button that opened it rather than letting it fall out of the panel.
        <div
            className='vntv-sched-form'
            onKeyDown={(event) => {
                if (event.key !== 'Escape' || composing(event)) {
                    return;
                }

                event.stopPropagation();
                if (picking) {
                    closePicker();
                    return;
                }

                onDone();
            }}
        >
            {plan ? (
                <p className='vntv-sched-editing'>
                    {'Changing '}
                    <b>{plan.title}</b>
                </p>
            ) : null}

            <TitleSearch
                kind={lookupKind}
                igdb={Boolean(me?.media?.igdb)}
                value={title}
                onChange={setTitle}
                picked={picked}
                onPick={setPicked}
                onSubmit={() => {
                    if (ready && !busy) {
                        save();
                    }
                }}
                before={(
                    <div
                        className='vntv-field-kinds'
                        role='group'
                        aria-label='What kind of stream'
                    >
                        {CATEGORIES.map(({value, label, Icon}) => {
                            const on = category === value;

                            return (
                                <button
                                    key={value}
                                    type='button'
                                    className='vntv-field-kind'
                                    aria-pressed={on}
                                    aria-label={label}
                                    title={on ? `${label} — press again to clear` : label}
                                    onClick={() => setCategory(on ? '' : value)}
                                ><Icon size={16}/></button>
                            );
                        })}
                    </div>
                )}
            />

            <div className='vntv-field'>
                <button
                    ref={whenRef}
                    type='button'
                    className='vntv-when-button vntv-field-when'
                    aria-expanded={picking}
                    onClick={() => setPicking((open) => !open)}
                >
                    <span
                        className='vntv-when-icon'
                        aria-hidden={true}
                    >{'🗓'}</span>
                    {formatWhen(startsAt, zone, now)}
                </button>
                <button
                    type='button'
                    className='vntv-field-save'
                    data-busy={busy ? 'true' : undefined}
                    disabled={busy || !ready}
                    aria-label={saveLabel}
                    title={saveLabel}
                    onClick={save}
                ><CheckIcon size={16}/></button>
            </div>

            {picking && (
                <WhenPicker
                    value={startsAt}
                    earliest={earliest}
                    latest={latest}
                    onChange={setStartsAt}
                    onClose={closePicker}
                />
            )}

            {error !== '' && <span className='vntv-hint vntv-notice-error'>{error}</span>}
        </div>
    );
};
