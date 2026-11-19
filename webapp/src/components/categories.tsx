import React from 'react';

import {GameIcon, MovieIcon} from './icons';

// What a streamer is planning, as one icon on the card.
//
// Three states, not two: a movie, a game, or nothing said. Nothing said is the
// ordinary case — it is what every entry planned before 2.0 carries, and what
// somebody who does not care to say gets — so it has no icon and no placeholder
// standing in for one.
//
// The value arrives from the server over JSON, so it is a string rather than a
// union: a document edited by hand, or written by a later build, can carry
// anything at all. Everything not listed here falls through to no icon.
export const CATEGORIES = [
    {value: 'movie', label: 'Movie', Icon: MovieIcon},
    {value: 'game', label: 'Game', Icon: GameIcon},
] as const;

export type Category = typeof CATEGORIES[number]['value'];

export function categoryOf(value?: string): typeof CATEGORIES[number] | null {
    return CATEGORIES.find((entry) => entry.value === value) || null;
}

// Decorative: the word it stands for is already in the card's own tooltip, so
// a screen reader that read the icon as well would say it twice.
export const CategoryIcon: React.FC<{category?: string; className: string; size?: number}> =
    ({category, className, size}) => {
        const found = categoryOf(category);
        if (!found) {
            return null;
        }

        return (
            <span
                className={className}
                aria-hidden={true}
            >
                <found.Icon size={size}/>
            </span>
        );
    };
