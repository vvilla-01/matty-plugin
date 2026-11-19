import React from 'react';

import {emojiKey, recentEmoji, searchEmoji, SUGGESTED} from '../emoji';
import {emojiURL} from '../emojiImages';
import {useMoreBelow} from '../moreBelow';
import type {SoundEmoji} from '../types';

import {SoundboardIcon} from './icons';

// An emoji's picture, from Mattermost's own origin, falling back to the
// speaker when a custom one has gone (design/3.2.0.md A13).
export const SoundEmojiImage: React.FC<{emoji: SoundEmoji; className?: string}> = ({emoji, className = 'vntv-snd-emoji'}) => {
    const [broken, setBroken] = React.useState(false);
    const url = emojiURL(emoji);

    React.useEffect(() => setBroken(false), [url]);

    if (broken) {
        return (
            <span
                className={`${className} vntv-snd-emoji-gone`}
                aria-hidden={true}
            ><SoundboardIcon size={18}/></span>
        );
    }

    return (
        <img
            className={className}
            src={url}
            alt=''
            draggable={false}
            onError={() => setBroken(true)}
        />
    );
};

// The chooser, in the add flow's place (design/3.2.0.md 7.13 step 3): a search
// driving Mattermost's own emoji search (system and the server's own, when its
// custom emoji are on), and before anything is typed the reader's RECENT emoji
// and forty SUGGESTED for sounds. A sequence number drops an answer to a query
// already typed over: the provider never cancels one.
export const EmojiChooser: React.FC<{chosen: SoundEmoji; onChoose: (emoji: SoundEmoji) => void}> = ({chosen, onChoose}) => {
    const [query, setQuery] = React.useState('');
    const [found, setFound] = React.useState<SoundEmoji[] | null>(null);
    const [recent, setRecent] = React.useState<SoundEmoji[]>([]);
    const sequence = React.useRef(0);
    const below = useMoreBelow();
    const field = React.useRef<HTMLInputElement | null>(null);

    React.useEffect(() => {
        let current = true;
        recentEmoji((list) => {
            if (current) {
                setRecent(list);
            }
        });
        field.current?.focus({preventScroll: true});

        return () => {
            current = false;
        };
    }, []);

    React.useEffect(() => {
        sequence.current += 1;
        const ask = sequence.current;
        if (query.trim().replace(/^:/, '').length < 2) {
            setFound(null);

            return;
        }
        searchEmoji(query, (list) => {
            if (ask === sequence.current) {
                setFound(list);
            }
        });
    }, [query]);

    const cell = (emoji: SoundEmoji) => (
        <button
            key={emojiKey(emoji)}
            type='button'
            className='vntv-snd-pick'
            data-chosen={emojiKey(emoji) === emojiKey(chosen) ? 'true' : undefined}
            aria-label={`:${emoji.name}:`}
            title={`:${emoji.name}:`}
            onClick={() => onChoose(emoji)}
        >
            <SoundEmojiImage
                emoji={emoji}
                className='vntv-snd-pick-image'
            />
        </button>
    );

    return (
        <div className='vntv-snd-chooser'>
            <input
                ref={field}
                className='vntv-input'
                type='search'
                value={query}
                placeholder='Search emoji'
                aria-label='Search emoji'
                autoComplete='off'
                spellCheck={false}
                onChange={(event) => setQuery(event.target.value)}
            />
            {found === null || found.length > 0 ? (
                <div
                    ref={below.ref}
                    className='vntv-snd-scroll vntv-snd-picks-scroll'
                    data-more={below.more ? 'true' : undefined}
                    onScroll={below.onScroll}
                >
                    {found !== null ? <div className='vntv-snd-picks'>{found.slice(0, 60).map(cell)}</div> : null}
                    {found === null && recent.length > 0 ? (
                        <>
                            <p className='vntv-snd-picks-head'>{'Recent'}</p>
                            <div className='vntv-snd-picks'>{recent.map(cell)}</div>
                        </>
                    ) : null}
                    {found === null ? (
                        <>
                            <p className='vntv-snd-picks-head'>{'Suggested'}</p>
                            <div className='vntv-snd-picks'>{SUGGESTED.map(cell)}</div>
                        </>
                    ) : null}
                </div>
            ) : null}
            {found !== null && found.length === 0 ? (
                <p className='vntv-snd-hint'>{`No emoji called “${query.trim()}”.`}</p>
            ) : null}
        </div>
    );
};
