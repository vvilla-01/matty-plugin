import React from 'react';

import {adminUnlink, fetchLetIn, letIn, letOut} from '../client';
import {now as clockNow, onClockAdvance} from '../clock';
import {useFocusHome} from '../focusHome';
import {currentUserId} from '../mattermost';
import {PRODUCT_NAME} from '../name';
import type {LetInList, LetInPerson} from '../types';

import {AdminCard, AdminHead, AdminPill, AdminStatus, useCardConfirm} from './adminParts';
import type {AdminCardProps} from './adminParts';
import {Avatar} from './Avatar';

// System Console > Plugins > Matty > Access (design/lock-screen.md 8.1):
// who may watch without a bearer token, and unlinking somebody's stream.
//
// Everybody, system admins included, sees the panel locked until they
// unlock it with the bearer token their streaming software publishes with. A
// system admin can let people in here instead: they may watch streams and
// clips, and nothing more. And can unlink somebody (design/lock-screen.md,
// question 2): revoking a token in Broadcast Box does not unlink anybody
// here, so without this a linked person could never be locked out again.
//
// Everything acts at once, like the artwork and the preview pictures: nothing
// waits for the page's Save button, and the list is not a setting (it is
// left out of the settings backup on purpose). Only admin routes, so it works
// for an admin whose own panel is locked. Loaded once, and again after each
// change; never polled.
//
// Drawn from the same parts as every other card (adminParts.tsx). Unlink
// takes a second press, as everything on the page that cannot be undone
// does; taking somebody off the list does not, because Undo puts them back.

// A row taken off the list stays, greyed, with Undo, for this long.
const UNDO_MS = 10000;

// Past this many people the list shows only its first ones, and a pill to show
// the rest. Full, it holds 500, which would put every section under Access
// some thirty thousand pixels down the page. Rows taken off a moment ago (with
// their Undo), the row with the keyboard focus and whoever was just let in are
// shown whatever their place, so nothing an admin has just done goes out of
// sight. Not remembered between visits, as the birthday list is not.
const FOLD_AFTER = 8;

// What the username boxes accept: @alice or alice, as Mattermost's own
// mentions are written. The server cleans it again.
function cleanUsername(raw: string): string {
    return raw.trim().replace(/^@/, '').toLowerCase();
}

function errorText(reason: unknown): string {
    return reason instanceof Error ? reason.message : String(reason);
}

function whenLetIn(at: number): string {
    if (!at) {
        return '';
    }

    try {
        return new Date(at).toLocaleDateString(undefined, {day: 'numeric', month: 'short', year: 'numeric'});
    } catch {
        return '';
    }
}

// Ids from a counter (see TitleSearch). Not vntv-, which the style check
// reads as a class.
let nextCard = 0;

type Removed = {person: LetInPerson; at: number};

// The server's order for the list (letin.go): by username, whatever its case,
// with deleted accounts last. A row taken off a moment ago is drawn back in
// the place it had by the same order, rather than at the end of the list,
// below the deleted accounts, away from the pointer that pressed Remove.
function listOrder(a: LetInPerson, b: LetInPerson): number {
    if ((a.state === 'gone') !== (b.state === 'gone')) {
        return a.state === 'gone' ? 1 : -1;
    }

    const aName = a.username.toLowerCase();
    const bName = b.username.toLowerCase();
    if (aName !== bName) {
        return aName < bName ? -1 : 1;
    }

    return a.userId < b.userId ? -1 : 1;
}

// A display name that only repeats the username is no name: the row would
// read "carol @carol". The server sends "" for it; this holds whatever
// case either is in.
function realName(person: LetInPerson): string {
    if (!person.name || (person.username && person.name.toLowerCase() === person.username.toLowerCase())) {
        return '';
    }

    return person.name;
}

// What Unlink does, always there under its heading rather than only during
// the few seconds its second press waits (section 8.1). It does not promise
// a lock: somebody on the let-in list keeps watching.
const UNLINK_HINT = 'Their stream stops counting as theirs at once. They can link it again with their bearer token unless you also revoke it in Broadcast Box, which by itself does not unlink anybody here.';

export const AdminAccess: React.FC<AdminCardProps> = ({id, label, disabled}) => {
    const [list, setList] = React.useState<LetInList | null>(null);
    const [loadError, setLoadError] = React.useState('');
    const [busy, setBusy] = React.useState('');
    const [error, setError] = React.useState('');
    const [name, setName] = React.useState('');
    const [unlinkName, setUnlinkName] = React.useState('');
    const [removed, setRemoved] = React.useState<Removed[]>([]);
    const [, setTick] = React.useState(0);
    const [unfolded, setUnfolded] = React.useState(false);
    const [lastAdded, setLastAdded] = React.useState('');
    const [retrying, setRetrying] = React.useState(false);

    // Which part of the card the last thing said belongs to: the list (Let
    // in, Remove, Undo) or Unlink. It is said under that part, beside the box
    // that was used, rather than in one place under Unlink, where a let-in's
    // answer read as an unlink's and, under a long list, was out of sight.
    const [part, setPart] = React.useState<'letin' | 'unlink'>('letin');
    const partNow = React.useRef(part);
    partNow.current = part;

    // What the last action did. An unlink of somebody still let in carries
    // its Remove with it, as part of the same note: whatever says something
    // new takes the pill away too, so a Remove never sits after somebody
    // else's news and takes the unlinked person off the list.
    const [note, setNoteState] = React.useState<{text: string; stillLetIn?: string}>({text: ''});
    const setNote = (text: string, stillLetIn?: string) => setNoteState({text, stillLetIn});

    // The pills in the list, by person, so focus can be put back on one; the
    // list's order as last drawn; and whose pill was pressed (or had focus as
    // its Undo lapsed), with the order then, until it is seen whether that
    // pill is still there.
    const rowButtons = React.useRef(new Map<string, HTMLButtonElement>());
    const addField = React.useRef<HTMLInputElement | null>(null);
    const foldToggle = React.useRef<HTMLButtonElement | null>(null);
    const foldedFromToggle = React.useRef(false);
    const shownOrder = React.useRef<string[]>([]);
    const pressed = React.useRef<{userId: string; order: string[]} | null>(null);
    const notePressed = (userId: string) => {
        pressed.current = {userId, order: shownOrder.current.slice()};
    };

    // The Remove under an unlink's note, pressed: it goes with the note, and
    // focus goes to that person's row, where Undo now is.
    const noteRemove = React.useRef<HTMLButtonElement | null>(null);
    const noteRemovePressed = React.useRef('');
    const me = currentUserId();
    const [ids] = React.useState(() => {
        nextCard += 1;

        return {
            add: `villanet-letin-add-${nextCard}`,
            list: `villanet-letin-list-${nextCard}`,
            unlink: `villanet-unlink-${nextCard}`,
            unlinkHint: `villanet-unlink-hint-${nextCard}`,
        };
    });

    // Only the newest request's answer is used. Somebody back on the list
    // (Undo) is no longer a row taken off.
    const latest = React.useRef(0);
    const load = React.useCallback(() => {
        const ticket = ++latest.current;

        return fetchLetIn().then((next) => {
            if (ticket === latest.current) {
                setList(next);
                setLoadError('');
                setRemoved((current) => current.filter((row) => !next.people.some((person) => person.userId === row.person.userId)));
            }
        }).catch((reason: unknown) => {
            if (ticket === latest.current) {
                setLoadError(errorText(reason));
            }
        });
    }, []);

    React.useEffect(() => {
        load();

        return () => {
            latest.current++;
        };
    }, [load]);

    // A removed row's Undo lapses after its ten seconds, by the lab's clock
    // too.
    const removedNow = React.useRef(removed);
    removedNow.current = removed;
    React.useEffect(() => {
        if (removed.length === 0) {
            return undefined;
        }

        const lapse = () => {
            const at = clockNow();
            const going = removedNow.current.filter((row) => at - row.at >= UNDO_MS).map((row) => row.person.userId);
            if (going.length === 0) {
                return;
            }

            const focused = going.find((userId) => rowButtons.current.get(userId) === document.activeElement);
            if (focused) {
                notePressed(focused);
            }
            setRemoved((current) => current.filter((row) => at - row.at < UNDO_MS));
        };
        const timer = window.setInterval(lapse, 1000);
        const unclock = onClockAdvance(() => {
            lapse();
            setTick((value) => value + 1);
        });

        return () => {
            window.clearInterval(timer);
            unclock();
        };
    }, [removed.length]);

    // A pill that went with what it did — Remove on a deactivated or deleted
    // account, which has no Undo, or an Undo that lapsed — took the keyboard
    // focus with it, to the page. It goes to the row that took its place, or
    // the one before, or the add box. Only if it fell to the page: an admin
    // who has since moved on is left where they are.
    //
    // Before the card's own focus home (useCardFocus, below), whose effect
    // runs after this one: a row to land on beats the status line, which is
    // where focus goes from anything this does not place.
    React.useLayoutEffect(() => {
        const fromNote = noteRemovePressed.current;
        if (fromNote !== '' && !noteRemove.current?.isConnected) {
            noteRemovePressed.current = '';
            const active = document.activeElement;
            if (!active || active === document.body) {
                const row = rowButtons.current.get(fromNote);
                (row?.isConnected ? row : addField.current)?.focus();
            }
        }

        const last = pressed.current;
        if (!last || rowButtons.current.get(last.userId)?.isConnected) {
            return;
        }

        pressed.current = null;
        const active = document.activeElement;
        if (active && active !== document.body) {
            return;
        }

        const index = last.order.indexOf(last.userId);
        const there = (userId: string) => userId !== last.userId && Boolean(rowButtons.current.get(userId)?.isConnected);
        const next = last.order.slice(index + 1).find(there) || last.order.slice(0, Math.max(index, 0)).reverse().find(there);
        const target = next ? rowButtons.current.get(next) : addField.current;
        target?.focus();
    });

    // Folded from the pill under a long list: the pill stays where the
    // keyboard is, and the page is brought to it, rather than left scrolled
    // far past the shorter list.
    React.useLayoutEffect(() => {
        if (foldedFromToggle.current) {
            foldedFromToggle.current = false;
            foldToggle.current?.scrollIntoView({block: 'nearest'});
        }
    });

    // Where focus goes when the pill that had it goes away (useCardFocus in
    // adminParts.tsx, here with a status line for each part): the line under
    // the part that was last used, which by then says what was done.
    const letInStatus = React.useRef<HTMLParagraphElement | null>(null);
    const unlinkStatus = React.useRef<HTMLParagraphElement | null>(null);
    const focus = useFocusHome(() => (partNow.current === 'unlink' ? unlinkStatus.current : letInStatus.current));

    // Unlink's second press. Arming it says, in Unlink's status line, what the
    // second press will do, in place of whatever was said last.
    const confirm = useCardConfirm(() => {
        setNote('');
        setError('');
        setPart('unlink');
    });

    // The let-in list saved by a newer version of the plugin, or a System Console this
    // admin may only read: shown, and nothing on the list can be changed.
    const readOnly = Boolean(list?.readOnly) || Boolean(disabled);

    // While a request is out, the pills are not pressed again — but they stay
    // focusable, marked aria-disabled: a disabled button drops the keyboard
    // focus it had, to the page.
    const waiting = busy !== '';

    const add = (raw: string, afterwards?: () => void, busyAs = 'add') => {
        const username = cleanUsername(raw);
        if (username === '' || waiting || readOnly) {
            if (username === '') {
                setPart('letin');
                setNote('');
                setError('Say who to let in.');
            }

            return;
        }

        setBusy(busyAs);
        setPart('letin');
        setNote('');
        setError('');
        confirm.disarm();
        letIn(username).then((answer) => {
            let said = answer.added ? `@${username} can now watch.` : `@${username} is already let in.`;
            if (answer.linked) {
                said += ' They have linked a stream, so this only matters if they unlink.';
            }
            setNote(said);
            setLastAdded(username);
            afterwards?.();
        }).catch((reason: unknown) => {
            setError(errorText(reason));
        }).finally(() => {
            setBusy('');
            load();
        });
    };

    // Said under the part it was pressed in: a row's Remove under the list's
    // line, the Remove under an unlink's note under Unlink's.
    const remove = (person: LetInPerson, from: 'letin' | 'unlink' = 'letin') => {
        if (waiting || readOnly) {
            return;
        }

        setBusy(`remove:${person.userId}`);
        setPart(from);
        setNote('');
        setError('');
        confirm.disarm();
        notePressed(person.userId);
        letOut(person.userId).then(() => {
            setRemoved((current) => [...current.filter((row) => row.person.userId !== person.userId), {person, at: clockNow()}]);
            setNote(`${person.username ? `@${person.username}` : 'They'} can no longer watch without a token.`);
        }).catch((reason: unknown) => {
            setError(errorText(reason));
        }).finally(() => {
            setBusy('');
            load();
        });
    };

    // Undo is a fresh let-in by username, which the server refuses for a
    // deactivated account and cannot do for a deleted one: offered only for
    // an active one. Once the list has them again, their row is where it
    // was, with Remove on the same button, so focus stays on it.
    const undo = (row: Removed) => {
        if (waiting || readOnly) {
            return;
        }

        notePressed(row.person.userId);
        add(row.person.username, undefined, `undo:${row.person.userId}`);
    };

    // Two presses, the second within a few seconds: the first turns the pill
    // red and says in the status line what the second does. Enter in the box
    // presses it.
    const unlink = (event: {detail: number}) => {
        const username = cleanUsername(unlinkName);
        if (username === '') {
            setPart('unlink');
            setNote('');
            setError('Say whose stream to unlink.');

            return;
        }
        if (waiting || disabled || !confirm.press('unlink', `Click again to unlink @${username}.`, event)) {
            return;
        }

        setBusy('unlink');
        setPart('unlink');
        setNote('');
        setError('');
        adminUnlink(username).then((answer) => {
            if (!answer.wasLinked) {
                setNote(`@${username} had no linked stream.`);
            } else if (answer.letIn) {
                setNote(`Unlinked. @${username} is still let in to watch.`, username);
            } else {
                setNote(`Unlinked. ${PRODUCT_NAME} is locked for @${username}.`);
            }
            setUnlinkName('');
        }).catch((reason: unknown) => {
            setError(errorText(reason));
        }).finally(() => {
            setBusy('');
            load();
        });
    };

    // Read again after a failure: the pill says Loading… until the answer,
    // and the card is drawn in full once there is a list.
    const retry = () => {
        if (retrying) {
            return;
        }

        setRetrying(true);
        load().finally(() => setRetrying(false));
    };

    if (list === null) {
        return (
            <AdminCard
                id={id}
                label={label}
                focus={focus}
            >
                <AdminHead instant={true}>{loadError === '' ? 'Loading…' : 'The let-in list could not be loaded.'}</AdminHead>
                {loadError === '' ? null : (
                    <div className='vntv-admin-actions'>
                        <AdminPill
                            waiting={retrying}
                            working={retrying}
                            workingWords='Loading…'
                            onPress={retry}
                        >{'Refresh'}</AdminPill>
                    </div>
                )}
                <AdminStatus
                    ref={letInStatus}
                    note=''
                    error={loadError}
                />
            </AdminCard>
        );
    }

    // People taken off a moment ago stay in the list, greyed, in the place
    // they had, until their Undo lapses.
    const shown: Array<{person: LetInPerson; gone?: Removed}> = list.people.map((person) => ({person}));
    removed.forEach((row) => {
        if (!list.people.some((person) => person.userId === row.person.userId)) {
            shown.push({person: row.person, gone: row});
        }
    });
    shown.sort((a, b) => listOrder(a.person, b.person));

    // Folded, the first rows, and whatever an admin has just touched, in its
    // place by the same order.
    const folded = !unfolded && shown.length > FOLD_AFTER;
    let focusedRow = '';
    rowButtons.current.forEach((button, userId) => {
        if (button === document.activeElement) {
            focusedRow = userId;
        }
    });
    const touched = (person: LetInPerson) => (
        person.userId === focusedRow ||
        person.userId === pressed.current?.userId ||
        removed.some((row) => row.person.userId === person.userId) ||
        (lastAdded !== '' && person.username.toLowerCase() === lastAdded)
    );
    const drawn = folded ? shown.filter(({person}, index) => index < FOLD_AFTER || touched(person)) : shown;
    shownOrder.current = drawn.map(({person}) => person.userId);

    // What went wrong reading the list again after a change, said with the
    // rest of what that change did.
    const problem = error || (loadError === '' ? '' : `The let-in list could not be loaded: ${loadError}`);

    // The let-in person named in the unlink the note is about, for its
    // Remove under it.
    const stillLetIn = note.stillLetIn ? list.people.find((person) => person.username.toLowerCase() === note.stillLetIn) : undefined;

    return (
        <AdminCard
            id={id}
            label={label}
            focus={focus}
        >
            {list.problem ? (
                <div
                    className='vntv-admin-callout'
                    data-tone='error'
                    role='alert'
                >
                    <p className='vntv-admin-line'>{list.problem}</p>
                </div>
            ) : null}

            <AdminHead instant={true}>
                {list.count === 0 ? 'Nobody is let in. Everybody needs their own bearer token.' : (
                    <><b>{`${list.count} of ${list.max}`}</b>{' let in without a token'}</>
                )}
            </AdminHead>

            <div className='vntv-access-entry'>
                <label
                    className='vntv-sr-only'
                    htmlFor={ids.add}
                >{'Username to let in'}</label>
                <input
                    ref={addField}
                    id={ids.add}
                    className='vntv-input'
                    type='text'
                    placeholder='username'
                    autoComplete='off'
                    spellCheck={false}
                    value={name}
                    disabled={readOnly}
                    onChange={(event) => setName(event.target.value)}
                    onKeyDown={(event) => {
                        if (event.key === 'Enter') {
                            event.preventDefault();
                            add(name, () => setName(''));
                        }
                    }}
                />
                <AdminPill
                    tone='primary'
                    off={readOnly}
                    waiting={waiting}
                    working={busy === 'add'}
                    workingWords='Saving…'
                    onPress={() => add(name, () => setName(''))}
                >{'Let in'}</AdminPill>
            </div>

            <AdminStatus
                ref={letInStatus}
                note={part === 'letin' ? note.text : ''}
                error={part === 'letin' ? problem : ''}
            />

            {drawn.length > 0 ? (
                <ul
                    id={ids.list}
                    className='vntv-admin-list'
                >
                    {drawn.map(({person, gone}) => {
                        const named = realName(person);
                        const display = named || (person.username ? `@${person.username}` : person.userId);
                        const keepButton = (element: HTMLButtonElement | null) => {
                            if (element) {
                                rowButtons.current.set(person.userId, element);
                            }
                        };
                        const by = person.byUsername ? ` by @${person.byUsername}` : '';
                        const when = whenLetIn(person.at);

                        return (
                            <li
                                key={person.userId}
                                className='vntv-admin-row'
                                data-gone={gone ? 'true' : undefined}
                            >
                                <span
                                    className='vntv-admin-thumb'
                                    data-shape='circle'
                                    data-size='small'
                                >
                                    <Avatar
                                        userId={person.state === 'gone' ? undefined : person.userId}
                                        name={display}
                                        className='vntv-admin-thumb-image'
                                        decorative={true}
                                    />
                                </span>
                                <div className='vntv-admin-row-main'>
                                    <p className='vntv-admin-row-title'>
                                        <span className='vntv-admin-row-name'>{display}</span>
                                        {named && person.username ? <span className='vntv-access-handle'>{`@${person.username}`}</span> : null}
                                        {person.userId === me ? <span className='vntv-access-handle'>{'(you)'}</span> : null}
                                        {person.state === 'deactivated' ? <span className='vntv-admin-flag'>{'Deactivated'}</span> : null}
                                        {person.state === 'gone' ? <span className='vntv-admin-flag'>{'Account deleted'}</span> : null}
                                    </p>
                                    <p className='vntv-admin-row-meta'>{gone ? 'Taken off the list.' : `Let in${when ? ` ${when}` : ''}${by}`}</p>
                                </div>
                                {/* One pill in this place, whichever it is,
                                    so focus stays on it as Remove turns into
                                    Undo and back. */}
                                {gone ? (
                                    person.state === 'active' && person.username ? (
                                        <AdminPill
                                            buttonRef={keepButton}
                                            off={readOnly}
                                            waiting={waiting}
                                            working={busy === `undo:${person.userId}`}
                                            workingWords='Saving…'
                                            label={`Undo: let ${display} in again`}
                                            onPress={() => undo(gone)}
                                        >{'Undo'}</AdminPill>
                                    ) : null
                                ) : (
                                    <AdminPill
                                        buttonRef={keepButton}
                                        off={readOnly}
                                        waiting={waiting}
                                        working={busy === `remove:${person.userId}`}
                                        workingWords='Removing…'
                                        label={`Take ${display} off the list`}
                                        onPress={() => remove(person)}
                                    >{'Remove'}</AdminPill>
                                )}
                            </li>
                        );
                    })}
                </ul>
            ) : null}

            {shown.length > FOLD_AFTER ? (
                <div className='vntv-admin-actions'>
                    <button
                        ref={foldToggle}
                        type='button'
                        className='vntv-pill'
                        aria-expanded={!folded}
                        aria-controls={ids.list}
                        onClick={() => {
                            if (!folded) {
                                foldedFromToggle.current = true;
                            }
                            setUnfolded(folded);
                        }}
                    >{folded ? `Show all ${shown.length}` : 'Show fewer'}</button>
                </div>
            ) : null}

            <h4 className='vntv-admin-sub'>{'Unlink somebody’s stream'}</h4>
            <p
                id={ids.unlinkHint}
                className='vntv-admin-line vntv-admin-quiet'
            >{UNLINK_HINT}</p>
            <div className='vntv-access-entry'>
                <label
                    className='vntv-sr-only'
                    htmlFor={ids.unlink}
                >{'Username to unlink'}</label>
                <input
                    id={ids.unlink}
                    className='vntv-input'
                    type='text'
                    placeholder='username'
                    autoComplete='off'
                    spellCheck={false}
                    value={unlinkName}
                    disabled={disabled}
                    aria-describedby={ids.unlinkHint}
                    onChange={(event) => {
                        setUnlinkName(event.target.value);
                        confirm.disarm();
                    }}
                    onKeyDown={(event) => {
                        if (event.key === 'Enter') {
                            event.preventDefault();
                            unlink({detail: 0});
                        }
                    }}
                />
                <AdminPill
                    off={disabled}
                    waiting={waiting}
                    working={busy === 'unlink'}
                    workingWords='Unlinking…'
                    confirms={true}
                    armed={confirm.armed === 'unlink'}
                    describedBy={ids.unlinkHint}
                    onPress={unlink}
                >{'Unlink'}</AdminPill>
            </div>

            <AdminStatus
                ref={unlinkStatus}
                note={confirm.hint || (part === 'unlink' ? note.text : '')}
                error={part === 'unlink' ? problem : ''}
            />

            {/* The unlink's own Remove, under the note that says they are
                still let in. */}
            {stillLetIn && note.text !== '' ? (
                <div className='vntv-admin-actions'>
                    <AdminPill
                        buttonRef={noteRemove}
                        off={readOnly}
                        waiting={waiting}
                        working={busy === `remove:${stillLetIn.userId}`}
                        workingWords='Removing…'
                        label={`Take @${stillLetIn.username} off the let-in list`}
                        onPress={() => {
                            if (!waiting && !readOnly) {
                                noteRemovePressed.current = stillLetIn.userId;
                                remove(stillLetIn, 'unlink');
                            }
                        }}
                    >{`Remove @${stillLetIn.username}`}</AdminPill>
                </div>
            ) : null}
        </AdminCard>
    );
};
