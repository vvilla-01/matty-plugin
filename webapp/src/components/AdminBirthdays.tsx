import React from 'react';

import {AdminCard, AdminPill, AdminStatus, useCardFocus} from './adminParts';

// The birthday list, edited in System Console > Plugins > Matty > Birthdays.
//
// Admin-only by construction rather than by a check: this is a plugin setting,
// and the System Console is behind manage_system. There is no endpoint, no
// panel surface and nothing a member can reach — adding, editing and removing a
// birthday all happen here or not at all.
//
// The value Mattermost stores is a JSON string, deliberately the same shape as
// the birthdays.json the old cron script read, so a list can be moved across by
// pasting it and nothing is trapped in here.

type Entry = {
    name: string;
    birthday: string;
    birth_year: number;
    username?: string;
    parent_username?: string | string[];
};

type Props = {
    id: string;
    label?: string;
    value?: string;
    disabled?: boolean;
    onChange?: (id: string, value: string) => void;
    setSaveNeeded?: () => void;
};

const MONTHS = [
    'January', 'February', 'March', 'April', 'May', 'June',
    'July', 'August', 'September', 'October', 'November', 'December',
];

// No starting list is offered: the plugin ships nobody's name or date of
// birth, so a list is typed in here, pasted, or restored from this server's
// own settings backup.

// 2020 is a leap year, so February offers the 29th to somebody born on it.
const daysIn = (month: number) => new Date(2020, month, 0).getDate();

const pad = (value: number) => String(value).padStart(2, '0');

function parse(value?: string): {entries: Entry[]; broken: string} {
    const source = (value || '').trim();
    if (source === '') {
        return {entries: [], broken: ''};
    }

    try {
        const parsed = JSON.parse(source);
        if (!Array.isArray(parsed)) {
            return {entries: [], broken: 'The saved list is not a JSON array.'};
        }

        return {entries: parsed as Entry[], broken: ''};
    } catch (reason) {
        return {entries: [], broken: reason instanceof Error ? reason.message : String(reason)};
    }
}

const parentsOf = (entry: Entry): string[] => {
    if (Array.isArray(entry.parent_username)) {
        return entry.parent_username.filter(Boolean);
    }

    return entry.parent_username ? [entry.parent_username] : [];
};

// What is wrong with a row, in the words somebody would use about it.
function complaint(entry: Entry): string {
    if (!entry.name?.trim()) {
        return 'This one needs a name.';
    }
    if (!(/^\d{2}-\d{2}$/).test(entry.birthday || '')) {
        return 'This one needs a date.';
    }
    if (!entry.username?.trim() && parentsOf(entry).length === 0) {
        return 'Pick an account, or name a parent for somebody who has none.';
    }

    return '';
}

// The age they are turning, and how long until they turn it — the two things
// somebody checks when they look at a list like this. Kept as parts rather than
// a sentence, so the summary over a folded list can pick whoever is soonest and
// word it around their name.
function upcoming(entry: Entry): {days: number; turning: number; when: string} | null {
    const month = Number(entry.birthday?.slice(0, 2));
    const day = Number(entry.birthday?.slice(3));
    if (!month || !day) {
        return null;
    }

    const now = new Date();
    let when = new Date(now.getFullYear(), month - 1, day);
    if (when < new Date(now.getFullYear(), now.getMonth(), now.getDate())) {
        when = new Date(now.getFullYear() + 1, month - 1, day);
    }

    const turning = entry.birth_year > 0 ? when.getFullYear() - entry.birth_year : 0;
    const days = Math.round((when.getTime() - now.getTime()) / 86400000);

    return {days, turning, when: days === 0 ? 'today' : `in ${days} ${days === 1 ? 'day' : 'days'}`};
}

function nextBirthday(entry: Entry): string {
    const next = upcoming(entry);
    if (!next) {
        return '';
    }

    return next.turning > 0 ? `Turns ${next.turning} ${next.when}` : next.when;
}

// "Alice Cole turns 35 in 3 days", for whoever on the list is soonest. Rows
// that still need fixing are left out: their date may be a placeholder.
function soonest(entries: Entry[]): string {
    let best: {name: string; next: NonNullable<ReturnType<typeof upcoming>>} | null = null;
    for (const entry of entries) {
        const next = complaint(entry) === '' ? upcoming(entry) : null;
        if (next && (!best || next.days < best.next.days)) {
            best = {name: entry.name.trim(), next};
        }
    }

    if (!best) {
        return '';
    }

    return best.next.turning > 0 ?
        `${best.name} turns ${best.next.turning} ${best.next.when}` :
        `${best.name}, ${best.next.when}`;
}

// Past this many people the list starts folded away behind its summary. A
// handful of cards is shorter than the fold is worth; a long list is what made
// scrolling past this setting to the ones under it a chore. Not remembered
// between visits on purpose: somebody who opened it once to add a name should
// not find it sprawled across the page every time after.
const FOLD_AFTER = 4;

// Ids for the list, unique per editor, so the Show button can say what it
// controls. From a counter (see TitleSearch); and not vntv-, which the style
// check reads as a class name.
let nextListId = 0;

export const AdminBirthdays: React.FC<Props> = ({id, label, value, disabled, onChange, setSaveNeeded}) => {
    const {entries, broken} = parse(value);
    const [note, setNote] = React.useState('');
    const [error, setError] = React.useState('');

    // A person's card goes when they are removed: focus then goes to the
    // line that says what happened, not to the top of the page.
    const {statusRef, focus} = useCardFocus();

    const commit = React.useCallback((next: Entry[]) => {
        if (onChange) {
            onChange(id, next.length === 0 ? '' : JSON.stringify(next, null, 2));
        }
        if (setSaveNeeded) {
            setSaveNeeded();
        }
    }, [id, onChange, setSaveNeeded]);

    const edit = (index: number, change: Partial<Entry>) => {
        commit(entries.map((entry, at) => (at === index ? {...entry, ...change} : entry)));
    };

    const [listId] = React.useState(() => {
        nextListId += 1;

        return `villanet-birthdays-${nextListId}`;
    });

    // Decided once, from the list as it was when the page opened: a list that
    // grows past the fold while somebody is typing into it stays open.
    const [open, setOpen] = React.useState(() => entries.length <= FOLD_AFTER);

    const listRef = React.useRef<HTMLDivElement>(null);
    const toggleRef = React.useRef<HTMLButtonElement>(null);

    // The row to put the cursor in once it exists. Added from a folded list,
    // the new card appears only after the list opens and the new value comes
    // back through Mattermost, so this waits for the list to reach that length
    // rather than assuming both land in the same render.
    const [focusAt, setFocusAt] = React.useState(-1);
    React.useEffect(() => {
        if (focusAt < 0 || entries.length <= focusAt) {
            return;
        }

        setFocusAt(-1);
        listRef.current?.children[focusAt]?.querySelector('input')?.focus();
    }, [focusAt, entries.length]);

    // Folding from the button under a long list would leave the cursor on a
    // button that no longer has anything above it to make sense of, and the
    // page scrolled far past the setting; the one at the top is where the
    // list is now.
    const fold = () => {
        setOpen(false);
        toggleRef.current?.focus();
    };

    // A list that will not parse is never silently replaced: an admin can see
    // what is stored and fix it, rather than losing it to an editor that
    // decided it was empty.
    if (broken !== '') {
        return (
            <AdminCard
                id={id}
                label={label}
            >
                <p className='vntv-admin-line vntv-notice-error'>
                    {'The saved birthday list could not be read: '}{broken}
                </p>
                <p className='vntv-admin-line'>
                    {'It is left exactly as it is. Correct it below and save.'}
                </p>
                <textarea
                    className='vntv-bd-raw'
                    defaultValue={value}
                    disabled={disabled}
                    onChange={(event) => {
                        if (onChange) {
                            onChange(id, event.target.value);
                        }
                        if (setSaveNeeded) {
                            setSaveNeeded();
                        }
                    }}
                />
            </AdminCard>
        );
    }

    const fixing = entries.filter((entry) => complaint(entry) !== '').length;
    const next = soonest(entries);

    return (
        <AdminCard
            id={id}
            label={label}
            focus={focus}
        >
            {entries.length === 0 && (
                <div className='vntv-admin-head'>
                    <p className='vntv-admin-stat'>{'Nobody on the list yet.'}</p>
                </div>
            )}

            {/* The list in one line, over it whether it is open or not, so a
                folded list still says what is in it and whether anything
                needs attention. Never disabled: looking is not editing. */}
            {entries.length > 0 && (
                <div className='vntv-admin-head'>
                    <p className='vntv-admin-stat'>
                        <b>{`${entries.length} on the list`}</b>
                        {next === '' ? '' : ` · next: ${next}`}
                        {fixing > 0 && (
                            <span className='vntv-notice-error'>
                                {` · ${fixing} ${fixing === 1 ? 'needs' : 'need'} fixing`}
                            </span>
                        )}
                    </p>
                    <button
                        ref={toggleRef}
                        type='button'
                        className='vntv-pill'
                        aria-expanded={open}
                        aria-controls={listId}
                        onClick={() => setOpen(!open)}
                    >{open ? 'Hide the list' : 'Show the list'}</button>
                </div>
            )}

            {/* Folded means not rendered at all, not merely hidden: with a
                long list that is hundreds of inputs and a 31-option select per
                person that the page no longer has to lay out. */}
            <div
                id={listId}
                ref={listRef}
                className='vntv-bd-list'
                hidden={!open}
            >
                {open && entries.map((entry, index) => {
                    const month = Number(entry.birthday?.slice(0, 2)) || 1;
                    const day = Number(entry.birthday?.slice(3)) || 1;
                    const parents = parentsOf(entry);
                    const hasAccount = !parents.length;
                    const wrong = complaint(entry);

                    return (
                        <div
                            key={index}
                            className='vntv-bd-card'
                        >
                            <div className='vntv-bd-line'>
                                <label className='vntv-bd-field vntv-bd-grow'>
                                    <span className='vntv-bd-label'>{'Name'}</span>
                                    <input
                                        className='vntv-input'
                                        placeholder='Full name'
                                        value={entry.name || ''}
                                        disabled={disabled}
                                        onChange={(event) => edit(index, {name: event.target.value})}
                                    />
                                </label>

                                {/* A DIV, not a label. A label is associated with
                                    its first labelable descendant and forwards
                                    every click inside itself to that one control —
                                    so wrapping the month, the day and the year in
                                    one label made the day and the year unclickable
                                    and sent every press to the month. Each control
                                    carries its own aria-label instead. */}
                                <div className='vntv-bd-field'>
                                    <span
                                        className='vntv-bd-label'
                                        aria-hidden={true}
                                    >{'Born'}</span>
                                    <span className='vntv-bd-date'>
                                        <select
                                            className='vntv-select'
                                            aria-label='Month of birth'
                                            value={month}
                                            disabled={disabled}
                                            onChange={(event) => {
                                                const next = Number(event.target.value);

                                                // Moving to a shorter month must
                                                // not leave a 31st behind.
                                                edit(index, {
                                                    birthday: `${pad(next)}-${pad(Math.min(day, daysIn(next)))}`,
                                                });
                                            }}
                                        >
                                            {MONTHS.map((name, at) => (
                                                <option
                                                    key={name}
                                                    value={at + 1}
                                                >{name}</option>
                                            ))}
                                        </select>
                                        <select
                                            className='vntv-select vntv-bd-day'
                                            aria-label='Day of birth'
                                            value={day}
                                            disabled={disabled}
                                            onChange={(event) => edit(index, {
                                                birthday: `${pad(month)}-${pad(Number(event.target.value))}`,
                                            })}
                                        >
                                            {Array.from({length: daysIn(month)}, (_, at) => at + 1).map((number) => (
                                                <option
                                                    key={number}
                                                    value={number}
                                                >{number}</option>
                                            ))}
                                        </select>
                                        <input
                                            className='vntv-input vntv-bd-year'
                                            type='number'
                                            min={1900}
                                            max={new Date().getFullYear()}
                                            aria-label='Year of birth'
                                            placeholder='Year'
                                            value={entry.birth_year || ''}
                                            disabled={disabled}
                                            onChange={(event) => edit(index, {birth_year: Number(event.target.value)})}
                                        />
                                    </span>
                                </div>
                            </div>

                            <div className='vntv-bd-line'>
                                <span className='vntv-bd-field'>
                                    <span className='vntv-bd-label'>{'Who'}</span>
                                    <span className='vntv-bd-who'>
                                        <button
                                            type='button'
                                            className='vntv-pill'
                                            aria-pressed={hasAccount}
                                            disabled={disabled}
                                            onClick={() => edit(index, {parent_username: undefined, username: entry.username || ''})}
                                        >{'Has an account'}</button>
                                        <button
                                            type='button'
                                            className='vntv-pill'
                                            aria-pressed={!hasAccount}
                                            disabled={disabled}
                                            onClick={() => edit(index, {username: '', parent_username: parents.length ? parents : ['']})}
                                        >{'A child'}</button>
                                    </span>
                                </span>

                                <label className='vntv-bd-field vntv-bd-grow'>
                                    <span className='vntv-bd-label'>
                                        {hasAccount ? 'Mattermost username' : 'Parents, comma separated'}
                                    </span>
                                    {hasAccount ? (
                                        <input
                                            className='vntv-input'
                                            placeholder='username'
                                            value={entry.username || ''}
                                            disabled={disabled}
                                            onChange={(event) => edit(index, {
                                                username: event.target.value.replace(/^@/, ''),
                                            })}
                                        />
                                    ) : (
                                        <input
                                            className='vntv-input'
                                            placeholder='parent, other-parent'
                                            value={parents.join(', ')}
                                            disabled={disabled}
                                            onChange={(event) => {
                                                const named = event.target.value.
                                                    split(',').
                                                    map((name) => name.trim().replace(/^@/, ''));

                                                edit(index, {parent_username: named});
                                            }}
                                        />
                                    )}
                                </label>
                            </div>

                            <div className='vntv-bd-foot'>
                                <span className={wrong === '' ? 'vntv-bd-when' : 'vntv-bd-when vntv-notice-error'}>
                                    {wrong === '' ? nextBirthday(entry) : wrong}
                                </span>
                                <AdminPill
                                    off={disabled}
                                    label={`Remove ${entry.name || 'this row'}`}
                                    onPress={() => {
                                        setError('');
                                        setNote(`Removed ${(entry.name || '').trim() || 'that row'}. Save the page to keep the change.`);
                                        commit(entries.filter((_, at) => at !== index));
                                    }}
                                >{'Remove'}</AdminPill>
                            </div>
                        </div>
                    );
                })}
            </div>

            <div className='vntv-admin-actions'>
                <button
                    type='button'
                    className='vntv-pill'
                    data-tone='primary'
                    disabled={disabled}
                    onClick={() => {
                        // Opens a folded list: a new blank card nobody can see
                        // would be added and then forgotten.
                        setNote('');
                        setOpen(true);
                        setFocusAt(entries.length);
                        commit(entries.concat({
                            name: '',
                            birthday: `${pad(new Date().getMonth() + 1)}-${pad(new Date().getDate())}`,
                            birth_year: 1990,
                            username: '',
                        }));
                    }}
                >{'Add somebody'}</button>

                {/* Under a long open list, a second way to fold it, so getting
                    past it does not mean scrolling back up to the top first. */}
                {open && entries.length > FOLD_AFTER && (
                    <button
                        type='button'
                        className='vntv-pill'
                        aria-expanded={true}
                        aria-controls={listId}
                        onClick={fold}
                    >{'Hide the list'}</button>
                )}
            </div>

            <AdminStatus
                ref={statusRef}
                note={note}
                error={error}
            />
        </AdminCard>
    );
};
