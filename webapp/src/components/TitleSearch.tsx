import React from 'react';

import {mediaImageURL, searchMedia} from '../client';
import {composing} from '../keys';
import {artShape, mediaSourceOf} from '../mediaRefs';
import type {MediaResult} from '../types';

import {CloseIcon} from './icons';

// The title box, when the plan is a movie or a game: it looks the title up as
// you type and offers the real ones.
//
// Picking one is never required. The words typed are the plan's title either
// way; a pick links the plan to that movie, show or game, which is what puts
// its art behind the card and its profile on it. The last suggestion says so
// in so many words — keep what I typed — so nobody has to guess that Escape
// or clicking away is allowed.
//
// A list, not a floating menu: it opens in place under the box and pushes the
// time down while it is open. The panel is a narrow scrolling column inside a
// row that slides shut, and anything positioned over it gets clipped by
// whichever of those is in the way that day. The calendar does the same.
//
// The keyboard does what it does in any search box: the arrows move through
// the suggestions, Enter takes the one highlighted (or saves, when none is),
// Escape closes the list and leaves the box as it is. It is a combobox to a
// screen reader, with the highlighted suggestion announced as it moves and
// the number of suggestions announced when they arrive.

type Props = {
    // What to look up; null when this plan is neither, or the server cannot.
    kind: 'movie' | 'game' | null;

    // Games come from IGDB too, for the ones Steam does not have: the admin
    // has set up a Twitch application. Only changes what the list says it
    // searched; the server decides where the results come from.
    igdb?: boolean;

    value: string;
    onChange: (next: string) => void;

    picked: MediaResult | null;
    onPick: (next: MediaResult | null) => void;

    // Enter with nothing highlighted: the form's own "save".
    onSubmit: () => void;

    // Rendered inside the joined field, before the box: the movie and game
    // toggles.
    before?: React.ReactNode;

    // And after it: the footer's What's on? puts its save button here, since
    // it has no second row to put one on.
    after?: React.ReactNode;
};

// Ids for the list and its options, unique per box. From a module counter
// rather than React.useId (which Mattermost 11.11's React 18 does have):
// plain ids read well in the page and need no escaping in a selector, where
// useId's colons would. Every other id counter in the plugin is there for
// the same reason.
let nextListId = 0;

// Long enough to not search on every keystroke, short enough to feel live.
const DEBOUNCE_MS = 260;
const MIN_QUERY = 2;

// An answer missing one of its sources (IGDB slow to answer, say) is asked
// for once more, this long after it came. The server goes on waiting for the
// late source by itself, and by then it usually has its answer.
const RETRY_MS = 1500;

type Found = {
    query: string;
    results: MediaResult[];
    unavailable?: string;

    // The source missing from these results, 'Steam' or 'IGDB'.
    incomplete?: string;
};

export const TitleSearch: React.FC<Props> = ({kind, igdb, value, onChange, picked, onPick, onSubmit, before, after}) => {
    const [found, setFound] = React.useState<Found | null>(null);
    const [loading, setLoading] = React.useState(false);
    const [failed, setFailed] = React.useState('');
    const [open, setOpen] = React.useState(false);
    const [active, setActive] = React.useState(-1);

    // Answers already had this session, so typing back over a query is
    // instant and costs the server nothing.
    const cache = React.useRef(new Map<string, Found>());
    const inputRef = React.useRef<HTMLInputElement | null>(null);
    const [listId] = React.useState(() => {
        nextListId += 1;

        return `villanet-titles-${nextListId}`;
    });

    const query = value.trim();
    const searching = kind !== null && picked === null;

    React.useEffect(() => {
        if (!searching || !kind || query.length < MIN_QUERY) {
            setFound(null);
            setLoading(false);
            setFailed('');

            return undefined;
        }

        const key = `${kind}|${query.toLowerCase()}`;
        const known = cache.current.get(key);
        if (known) {
            setFound(known);
            setLoading(false);
            setFailed('');

            return undefined;
        }

        setLoading(true);
        setFailed('');
        const controller = new AbortController();
        let retry = 0;

        // An answer missing one of its sources is shown but not kept, so the
        // same words typed again are asked again; and it is asked for once
        // more by itself, while the words stay the same.
        const ask = (again: boolean) => {
            searchMedia(kind, query, controller.signal).then((answer) => {
                const next: Found = {query, results: answer.results || [], unavailable: answer.unavailable, incomplete: answer.incomplete || undefined};
                if (!next.incomplete) {
                    cache.current.set(key, next);
                }

                // Already moved on: nothing to show, and no second try for
                // words no longer there.
                if (controller.signal.aborted) {
                    return;
                }
                if (next.incomplete && !again) {
                    retry = window.setTimeout(() => ask(true), RETRY_MS);
                }
                setFound(next);
            }).catch((reason: unknown) => {
                // A second try that fails leaves the first answer showing.
                if (controller.signal.aborted || again) {
                    return;
                }
                setFound(null);
                setFailed(reason instanceof Error ? reason.message : 'The lookup did not answer.');
            }).finally(() => {
                if (!controller.signal.aborted) {
                    setLoading(false);
                }
            });
        };

        const timer = window.setTimeout(() => ask(false), DEBOUNCE_MS);

        return () => {
            window.clearTimeout(timer);
            window.clearTimeout(retry);
            controller.abort();
        };
    }, [searching, kind, query]);

    // A new answer starts with nothing highlighted: Enter still saves until
    // somebody chooses to move into the list.
    React.useEffect(() => setActive(-1), [found]);

    const results = found?.results || [];

    // The options: every result, then "keep what I typed".
    const optionCount = results.length + 1;
    const showList = open && searching && query.length >= MIN_QUERY && (loading || failed !== '' || found !== null);

    const take = (result: MediaResult | null) => {
        setOpen(false);
        setActive(-1);
        if (result) {
            onChange(result.title);
            onPick(result);
        }
        inputRef.current?.focus();
    };

    const optionId = (index: number) => `${listId}-option-${index}`;

    // The highlight moves with the keyboard while focus stays in the box, so
    // the browser will not bring the highlighted suggestion into view by
    // itself. Nearest, so the panel around it does not jump.
    React.useEffect(() => {
        if (showList && active >= 0) {
            document.getElementById(`${listId}-option-${active}`)?.scrollIntoView({block: 'nearest'});
        }
    }, [showList, active, listId]);
    // Where the list looked, for "Nothing on … called", and who did not answer
    // when it failed: with IGDB on, a failure means both did not.
    const source = kind === 'game' ? (igdb ? 'Steam or IGDB' : 'Steam') : 'TMDB';
    const asked = kind === 'game' ? (igdb ? 'Steam and IGDB' : 'Steam') : 'TMDB';

    // An answer with one source missing names only the one that answered:
    // "nothing on Steam or IGDB" would be wrong about the one that did not.
    const missing = found?.incomplete || '';
    const answeredBy = missing === 'Steam' ? 'IGDB' : 'Steam';
    const nothing = missing ?
        `Nothing on ${answeredBy} called “${query}”, and ${missing} did not answer just now.` :
        `Nothing on ${source} called “${query}”.`;

    const announcement = (() => {
        if (!showList || loading) {
            return '';
        }
        if (failed || found?.unavailable) {
            return failed || found?.unavailable || '';
        }

        if (results.length === 0) {
            return missing ? nothing : 'No matches';
        }

        return `${results.length} suggestion${results.length === 1 ? '' : 's'}`;
    })();

    return (
        // The list closes when focus leaves the whole thing — the box, the
        // movie and game toggles, the unlink button — and not when it merely
        // moves between them: pressing the film icon should show its results.
        <div
            className='vntv-titlesearch'
            onBlur={(event) => {
                if (!event.currentTarget.contains(event.relatedTarget as Node | null)) {
                    setOpen(false);
                }
            }}
        >
            <div className='vntv-field'>
                {before}
                <input
                    ref={inputRef}
                    className='vntv-input vntv-field-input'
                    value={value}
                    maxLength={80}
                    autoFocus={true}
                    placeholder={placeholderFor(kind)}
                    aria-label='What are you streaming?'
                    role={searching ? 'combobox' : undefined}
                    aria-autocomplete={searching ? 'list' : undefined}
                    aria-expanded={searching ? showList : undefined}
                    aria-controls={searching ? listId : undefined}
                    aria-activedescendant={showList && active >= 0 ? optionId(active) : undefined}
                    autoComplete='off'
                    spellCheck={false}
                    onChange={(event) => {
                        onChange(event.target.value);
                        setOpen(true);
                    }}
                    onFocus={() => setOpen(true)}
                    onKeyDown={(event) => {
                        if (composing(event)) {
                            return;
                        }

                        if (showList && (event.key === 'ArrowDown' || event.key === 'ArrowUp')) {
                            event.preventDefault();
                            // Round and round: past the last back to the
                            // first, and up from nothing to the last.
                            if (event.key === 'ArrowDown') {
                                setActive((current) => (current < optionCount - 1 ? current + 1 : 0));
                            } else {
                                setActive((current) => (current > 0 ? current - 1 : optionCount - 1));
                            }

                            return;
                        }

                        if (event.key === 'ArrowDown' && searching && !showList) {
                            event.preventDefault();
                            setOpen(true);

                            return;
                        }

                        if (event.key === 'Escape' && showList) {
                            // The list closes; the form stays open.
                            event.preventDefault();
                            event.stopPropagation();
                            setOpen(false);
                            setActive(-1);

                            return;
                        }

                        if (event.key === 'Enter') {
                            event.preventDefault();
                            if (showList && active >= 0 && active < results.length) {
                                take(results[active]);
                            } else if (showList && active === results.length) {
                                take(null);
                            } else {
                                onSubmit();
                            }
                        }
                    }}
                />
                {after}
            </div>

            {/* Linked: the pick, with a way to undo it. Typing no longer
                searches while a title is linked — change the words freely,
                "Movie night: Heat" is still Heat. */}
            {picked && (
                <div className='vntv-picked'>
                    <MediaThumb
                        result={picked}
                        className='vntv-picked-thumb'
                    />
                    <span className='vntv-picked-text'>
                        <span className='vntv-picked-title'>{picked.title}</span>
                        <span className='vntv-picked-meta'>{metaOf(picked)}</span>
                    </span>
                    <span className='vntv-picked-tag'>{'Linked'}</span>
                    <button
                        type='button'
                        className='vntv-picked-undo'
                        aria-label={`Unlink ${picked.title}`}
                        title='Unlink — keep the words, drop the movie or game'
                        onClick={() => {
                            onPick(null);
                            setOpen(true);
                            inputRef.current?.focus();
                        }}
                    ><CloseIcon size={12}/></button>
                </div>
            )}

            {showList && (
                <div
                    className='vntv-suggest'
                    onMouseDown={(event) => {
                        // Keeps focus in the box while an option is clicked.
                        event.preventDefault();
                    }}
                >
                    <p className='vntv-suggest-head'>
                        {kind === 'game' ? (igdb ? 'Games on Steam and IGDB' : 'Games on Steam') : 'Movies and shows on TMDB'}
                        {loading && <span className='vntv-suggest-spinner' aria-hidden={true}/>}
                    </p>

                    <ul
                        id={listId}
                        className='vntv-suggest-list'
                        role='listbox'
                        aria-label={kind === 'game' ? 'Games' : 'Movies and shows'}
                    >
                        {loading && results.length === 0 && [0, 1, 2].map((index) => (
                            <li
                                key={`wait-${index}`}
                                className='vntv-suggest-wait'
                                data-kind={kind || undefined}
                                aria-hidden={true}
                            >
                                <span className='vntv-suggest-wait-thumb'/>
                                <span className='vntv-suggest-wait-lines'>
                                    <span/>
                                    <span/>
                                </span>
                            </li>
                        ))}

                        {results.map((result, index) => (
                            <li
                                key={result.ref}
                                id={optionId(index)}
                                className='vntv-suggest-option'
                                role='option'
                                aria-selected={active === index}
                                data-active={active === index ? 'true' : undefined}
                                data-kind={result.kind}
                                onMouseEnter={() => setActive(index)}
                                onClick={() => take(result)}
                            >
                                <MediaThumb
                                    result={result}
                                    className='vntv-suggest-thumb'
                                />
                                <span className='vntv-suggest-text'>
                                    <span className='vntv-suggest-title'>{highlight(result.title, query)}</span>
                                    <span className='vntv-suggest-meta'>{metaOf(result)}</span>
                                </span>
                            </li>
                        ))}

                        {!loading && found && results.length === 0 && !found.unavailable && (
                            <li
                                className='vntv-suggest-note'
                                aria-hidden={true}
                            >{nothing}</li>
                        )}

                        {!loading && found && results.length > 0 && missing !== '' && (
                            <li
                                className='vntv-suggest-note'
                                aria-hidden={true}
                            >{`${missing} did not answer just now, so these are from ${answeredBy} alone.`}</li>
                        )}

                        {(failed || found?.unavailable) && (
                            <li
                                className='vntv-suggest-note'
                                aria-hidden={true}
                            >{found?.unavailable || `${asked} did not answer just now.`}</li>
                        )}

                        <li
                            id={optionId(results.length)}
                            className='vntv-suggest-option vntv-suggest-keep'
                            role='option'
                            aria-selected={active === results.length}
                            data-active={active === results.length ? 'true' : undefined}
                            onMouseEnter={() => setActive(results.length)}
                            onClick={() => take(null)}
                        >{`Keep “${query}” as my own title`}</li>
                    </ul>

                    <p className='vntv-suggest-foot'>
                        {'↑↓ to choose · Enter to link · Esc to keep typing'}
                    </p>
                </div>
            )}

            <span
                className='vntv-sr-only'
                aria-live='polite'
            >{announcement}</span>
        </div>
    );
};

// A result's art: a portrait poster for a movie or show, Steam's landscape
// capsule for a game, or IGDB's portrait box art for a game from there. A box
// of the same shape stands in while it loads, and stays if there is none.
const MediaThumb: React.FC<{result: MediaResult; className: string}> = ({result, className}) => {
    const [failed, setFailed] = React.useState(false);

    return (
        <span
            className={className}
            data-kind={result.kind}
            data-art={artShape(result.ref)}
            aria-hidden={true}
        >
            {!failed && (
                <img
                    src={mediaImageURL(result.ref, 'thumb')}
                    alt=''
                    loading='lazy'
                    draggable={false}
                    onError={() => setFailed(true)}
                />
            )}
        </span>
    );
};

// "Game · 2026 · IGDB": an IGDB title says where it came from in the list
// itself, as the Twitch Developer Agreement's attribution clause asks. Steam
// and TMDB titles leave it to their profiles, which say "Data from …".
function metaOf(result: MediaResult): string {
    return [result.label || labelOf(result.kind), result.year, mediaSourceOf(result.ref) === 'IGDB' ? 'IGDB' : ''].filter(Boolean).join(' · ');
}

function labelOf(kind: string): string {
    switch (kind) {
    case 'movie':
        return 'Movie';
    case 'tv':
        return 'TV series';
    case 'game':
        return 'Game';
    default:
        return '';
    }
}

function placeholderFor(kind: 'movie' | 'game' | null): string {
    switch (kind) {
    case 'movie':
        return 'Which movie or show?';
    case 'game':
        return 'Which game?';
    default:
        return 'What are you streaming?';
    }
}

// The part of a title that matches what was typed, picked out, so the eye
// finds why each suggestion is there. Case-insensitive; the first match only.
function highlight(title: string, query: string): React.ReactNode {
    const at = query ? title.toLowerCase().indexOf(query.toLowerCase()) : -1;
    if (at < 0) {
        return title;
    }

    return (
        <>
            {title.slice(0, at)}
            <mark className='vntv-suggest-match'>{title.slice(at, at + query.length)}</mark>
            {title.slice(at + query.length)}
        </>
    );
}
