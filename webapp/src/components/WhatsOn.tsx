import React from 'react';

import {mediaImageURL, saveNowPlaying} from '../client';
import {useIdentity} from '../identity';
import {composing} from '../keys';
import type {MediaResult, NowPlaying} from '../types';

import {CATEGORIES, CategoryIcon} from './categories';
import {CheckIcon, TvIcon} from './icons';
import {TitleSearch} from './TitleSearch';

// "What's on?": saying which movie, show or game you are streaming right now,
// from the footer's bar. For the stream nobody planned — somebody starts a
// game on a whim and goes live — so the people watching can see what it is
// without asking.
//
// Two pieces, because they live in two places. The button sits among the
// bar's tools: a small television while nothing is said, and a chip with what
// is said — its picture and its title — once it is. It is the same button
// either way, so focus on it survives a save. Pressing it opens the picker in
// a sheet above the bar (Sidebar.tsx, which also has Take down in the sheet's
// head): the planning form's own first row — the movie and game toggles joined
// to the title box, looking the title up as you type when the server can —
// with the save button joined to its right-hand end, since there is no second
// row to put one on. Nothing is typed into a time: it is now.
//
// It goes by itself once the stream has been off the air for a little while,
// so nobody has to remember to clear it; the button's tooltip and the sheet
// say so.

type ChipProps = {
    // What you said, as the server has it; null for nothing.
    nowPlaying: NowPlaying | null;

    // Whether the picker's sheet is open, and its id.
    open: boolean;
    pickerId: string;

    onOpen: () => void;

    // The button itself, for focus to come back to when the picker closes or
    // what was said is taken down.
    buttonRef: React.MutableRefObject<HTMLButtonElement | null>;
};

const HINT = 'Clears itself once your stream has been off the air for a moment.';

export const WhatsOnChip: React.FC<ChipProps> = ({nowPlaying, open, pickerId, onOpen, buttonRef}) => (
    <button
        ref={buttonRef}
        type='button'
        className='vntv-bar-tool vntv-whatson'
        data-set={nowPlaying ? 'true' : undefined}
        aria-expanded={open}
        aria-controls={open ? pickerId : undefined}
        title={nowPlaying ?
            `What’s on: ${nowPlaying.title}. Click to change it. ${HINT}` :
            `What’s on? Say which movie, show or game you are streaming right now. ${HINT}`}
        onClick={onOpen}
    >
        {nowPlaying ? <ChipArt nowPlaying={nowPlaying}/> : <TvIcon size={17}/>}

        {/* Its name starts the same way whether or not anything is said,
            and goes on to say what is. */}
        <span className='vntv-sr-only'>{nowPlaying ? 'What’s on: ' : 'What’s on?'}</span>
        {nowPlaying ? <span className='vntv-whatson-title'>{nowPlaying.title}</span> : null}
    </button>
);

// The chip's picture: the title's own art when there is one, and the kind's
// icon otherwise — or when the server will not relay it, as when lookups have
// been switched off since. Portrait for both kinds: a film's poster thumbnail,
// and a game's box art rather than Steam's wide capsule, which at this height
// took half the chip and left the title three letters.
const ChipArt: React.FC<{nowPlaying: NowPlaying}> = ({nowPlaying}) => {
    const [failed, setFailed] = React.useState(false);
    const ref = nowPlaying.media?.ref || '';
    const size = nowPlaying.media?.kind === 'game' ? 'cover' : 'thumb';

    React.useEffect(() => setFailed(false), [ref]);

    if (ref === '' || failed) {
        return (
            <CategoryIcon
                category={nowPlaying.category}
                className='vntv-whatson-kind'
                size={14}
            />
        );
    }

    return (
        <span
            className='vntv-whatson-thumb'
            aria-hidden={true}
        >
            <img
                src={mediaImageURL(ref, size)}
                alt=''
                draggable={false}
                onError={() => setFailed(true)}
            />
        </span>
    );
};

type PickerProps = {
    // What is said now, to start from: changing it, rather than starting
    // again.
    nowPlaying: NowPlaying | null;

    // Saved: what the server now has.
    onSaved: (next: NowPlaying | null) => void;

    // Finished with, saved or not: the footer closes it and puts focus back
    // on What's on? in the bar.
    onDone: () => void;
};

export const WhatsOnPicker: React.FC<PickerProps> = ({nowPlaying, onSaved, onDone}) => {
    const [category, setCategory] = React.useState(() => nowPlaying?.category || '');
    const [title, setTitle] = React.useState(() => nowPlaying?.title || '');
    const [busy, setBusy] = React.useState(false);
    const [error, setError] = React.useState('');

    // The title picked from the suggestions, if one was.
    const [picked, setPicked] = React.useState<MediaResult | null>(() => (nowPlaying?.media ? {
        ref: nowPlaying.media.ref,
        kind: nowPlaying.media.kind,
        title: nowPlaying.media.title,
        year: nowPlaying.media.year,
    } : null));

    // What the box can look up, exactly as in the planning form: movies and
    // shows under the film icon, games under the controller, and nothing at
    // all unless the server says it can — in which case the kind and the words
    // are still saved, just without a cover.
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

    // A game picked and then the film icon pressed: that game is not what is
    // on any more. By the kind, as in the planning form, not by whether
    // lookups are available.
    React.useEffect(() => {
        if (!picked) {
            return;
        }

        const fits = picked.kind === 'game' ? category === 'game' : category === 'movie';
        if (!fits) {
            setPicked(null);
        }
    }, [category, picked]);

    // Whether this picker is still the one on screen when a save lands.
    const alive = React.useRef(false);
    React.useEffect(() => {
        alive.current = true;

        return () => {
            alive.current = false;
        };
    }, []);

    // Unlike a plan, the kind is needed: without one this would only be a
    // second stream title, which the bar beside it already is.
    const ready = category !== '' && title.trim() !== '';

    const save = () => {
        if (busy) {
            return;
        }

        // Enter in the box with something missing says what, rather than
        // doing nothing: the save button's label is only seen by pointing.
        if (!ready) {
            setError(category === '' ? 'Choose the film or the game icon first.' : 'Say what is on first.');
            return;
        }

        setBusy(true);
        setError('');

        saveNowPlaying(category, title, picked?.ref).then((answer) => {
            onSaved(answer.nowPlaying);
            if (alive.current) {
                onDone();
            }
        }).catch((reason: unknown) => {
            if (alive.current) {
                setError(reason instanceof Error ? reason.message : String(reason));
            }
        }).finally(() => {
            if (alive.current) {
                setBusy(false);
            }
        });
    };

    const saveLabel = (() => {
        if (busy) {
            return 'Saving…';
        }
        if (category === '') {
            return 'Choose movie or game first';
        }
        if (title.trim() === '') {
            return 'Say what is on first';
        }

        return nowPlaying ? 'Save the change' : 'Show it on my stream';
    })();

    return (

        // Escape from anywhere in it closes it, as the planning form does —
        // after the suggestions, which close first and stop it there. Its
        // heading is the sheet's, around it.
        <div
            className='vntv-whatson-form'
            onKeyDown={(event) => {
                if (event.key !== 'Escape' || composing(event)) {
                    return;
                }

                event.preventDefault();
                event.stopPropagation();
                onDone();
            }}
        >
            <TitleSearch
                kind={lookupKind}
                igdb={Boolean(me?.media?.igdb)}
                value={title}
                onChange={(next) => {
                    setTitle(next);
                    setError('');
                }}
                picked={picked}
                onPick={setPicked}
                onSubmit={save}
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
                                    onClick={() => {
                                        setCategory(on ? '' : value);
                                        setError('');
                                    }}
                                ><Icon size={16}/></button>
                            );
                        })}
                    </div>
                )}
                after={(
                    <button
                        type='button'
                        className='vntv-field-save'
                        data-busy={busy ? 'true' : undefined}
                        disabled={busy || !ready}
                        aria-label={saveLabel}
                        title={saveLabel}
                        onClick={save}
                    ><CheckIcon size={16}/></button>
                )}
            />

            {error !== '' && <span className='vntv-hint vntv-notice-error'>{error}</span>}
        </div>
    );
};
