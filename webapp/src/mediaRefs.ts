// What a title's ref says about it, read from the ref alone: which service the
// server looked it up in, and what shape its art is. The server writes refs as
// tmdb:movie:949, tmdb:tv:1396, steam:1245620 or igdb:113360, and the panel
// never builds one itself, so the prefix is all there is to go on.

export type MediaSource = 'TMDB' | 'Steam' | 'IGDB';

export function mediaSourceOf(ref: string): MediaSource | '' {
    if (ref.startsWith('tmdb:')) {
        return 'TMDB';
    }
    if (ref.startsWith('steam:')) {
        return 'Steam';
    }
    if (ref.startsWith('igdb:')) {
        return 'IGDB';
    }

    return '';
}

// The shape of a title's thumb: Steam's capsule is wide, and everything else
// is portrait, a poster or IGDB's box art. Games used to mean wide, because
// every game came from Steam; an IGDB game is a game with a tall picture, so
// the stylesheet goes by this as well as by the kind.
export function artShape(ref: string): 'wide' | 'tall' {
    return ref.startsWith('steam:') ? 'wide' : 'tall';
}
