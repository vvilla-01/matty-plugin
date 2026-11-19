import type {MediaInfo, MediaResult} from '../src/types';

// The lab's stand-in for TMDB, Steam and IGDB: a handful of titles, and art
// drawn on a canvas so there is something to look at without a network. The
// shapes are the real ones — a portrait poster, a wide backdrop, a Steam
// game's landscape capsule, an IGDB game's portrait box art — because the
// layout has to cope with exactly those.

type Entry = MediaInfo & {label: string; hue: number};

const CATALOGUE: Entry[] = [
    {ref: 'tmdb:movie:949', kind: 'movie', title: 'Heat', year: '1995', label: 'Movie', hue: 18, source: 'TMDB',
        facts: 'Crime · Drama · Thriller · 2h 50m', link: 'https://www.imdb.com/title/tt0113277/',
        overview: 'Obsessive master thief Neil McCauley leads a top-notch crew on various daring heists throughout Los Angeles while determined detective Vincent Hanna pursues him without rest.'},
    {ref: 'tmdb:movie:136795', kind: 'movie', title: 'The Heat', year: '2013', label: 'Movie', hue: 330, source: 'TMDB',
        facts: 'Action · Comedy · Crime · 1h 57m', link: 'https://www.imdb.com/title/tt2404463/',
        overview: 'Uptight and straight-laced FBI Special Agent Sarah Ashburn is a methodical investigator with a reputation for excellence.'},
    {ref: 'tmdb:tv:1396', kind: 'tv', title: 'Breaking Bad', year: '2008', label: 'TV series', hue: 95, source: 'TMDB',
        facts: 'Drama · Crime · 5 seasons', link: 'https://www.imdb.com/title/tt0903747/',
        overview: 'Walter White, a New Mexico chemistry teacher, is diagnosed with Stage III cancer and given a prognosis of only two years left to live.'},
    {ref: 'tmdb:movie:603', kind: 'movie', title: 'The Matrix', year: '1999', label: 'Movie', hue: 130, source: 'TMDB',
        facts: 'Action · Science Fiction · 2h 16m', link: 'https://www.imdb.com/title/tt0133093/',
        overview: 'Set in the 22nd century, The Matrix tells the story of a computer hacker who joins a group of underground insurgents fighting the vast and powerful computers who now rule the earth.'},
    {ref: 'steam:1245620', kind: 'game', title: 'ELDEN RING', year: '2022', label: 'Game', hue: 42, source: 'Steam',
        facts: 'Action · RPG · FromSoftware, Inc.', link: 'https://store.steampowered.com/app/1245620/',
        overview: 'THE CRITICALLY ACCLAIMED FANTASY ACTION RPG. Rise, Tarnished, and be guided by grace to brandish the power of the Elden Ring and become an Elden Lord in the Lands Between.'},
    {ref: 'steam:2622380', kind: 'game', title: 'ELDEN RING NIGHTREIGN', year: '2025', label: 'Game', hue: 260, source: 'Steam',
        facts: 'Action · RPG · FromSoftware, Inc.', link: 'https://store.steampowered.com/app/2622380/',
        overview: 'A standalone adventure within the ELDEN RING universe, crafted to offer a new gameplay experience.'},
    {ref: 'steam:107410', kind: 'game', title: 'Arma 3', year: '2013', label: 'Game', hue: 75, source: 'Steam',
        facts: 'Action · Simulation · Strategy · Bohemia Interactive', link: 'https://store.steampowered.com/app/107410/',
        overview: 'Experience true combat gameplay in a massive military sandbox. Deploying a wide variety of single- and multiplayer content, over 20 vehicles and 40 weapons.'},
    {ref: 'steam:1874880', kind: 'game', title: 'Arma Reforger', year: '2022', label: 'Game', hue: 200, source: 'Steam',
        facts: 'Action · Simulation · Bohemia Interactive', link: 'https://store.steampowered.com/app/1874880/',
        overview: 'Fight for control of a vast, authentic Cold War-era island in this open-world military sandbox.'},

    // IGDB: games Steam does not sell. Hytale and Vintage Story have their
    // real IGDB ids; the others are lab-only. The facts line is the server's,
    // genre names shortened as igdbGenreName does ("Role-playing (RPG)" is
    // RPG). Elden Ring is here too, as IGDB lists it, so a search for it
    // shows the merge leaving it to Steam.
    {ref: 'igdb:113360', kind: 'game', title: 'Hytale', year: '2026', label: 'Game', hue: 160, source: 'IGDB',
        facts: 'RPG · Simulator · Hypixel Studios', link: 'https://www.igdb.com/games/hytale',
        overview: 'A block-built adventure game of exploration, crafting and combat in a procedurally generated world, with tools for players to make their own.'},
    {ref: 'igdb:69547', kind: 'game', title: 'Vintage Story', year: '2018', label: 'Game', hue: 30, source: 'IGDB',
        facts: 'Indie · Anego Studios', link: 'https://www.igdb.com/games/vintage-story',
        overview: 'A survival sandbox game about living off a harsh, changing land: seasons, temperature and hunger all matter, and crafting follows how things are really made.'},
    {ref: 'igdb:900001', kind: 'game', title: 'Star Citizen', year: '', label: 'Game', hue: 220, source: 'IGDB',
        facts: 'Simulator · Shooter · Cloud Imperium Games', link: 'https://www.igdb.com/games/star-citizen',
        overview: 'A space game still in development: fly, trade and fight across a persistent universe.'},
    {ref: 'igdb:900002', kind: 'game', title: 'Elden Ring', year: '2022', label: 'Game', hue: 42, source: 'IGDB',
        facts: 'RPG · Adventure · FromSoftware', link: 'https://www.igdb.com/games/elden-ring',
        overview: 'IGDB’s record of the game Steam also sells: never shown, because Steam lists it.'},
];

// How a query or a title is compared, as the server's mediaQueryKey does it:
// case, punctuation and spacing make no difference, and apostrophes vanish
// rather than split a word.
function queryKey(text: string): string {
    return text.toLowerCase().replace(/['’]/g, '').replace(/[^\p{L}\p{N}]+/gu, ' ').trim();
}

// Steam first, IGDB filling in, as the server's mergeGames does. The lab has
// no delisted games, so leaving to Steam what Steam listed is the title
// comparison alone.
function mergeGames(steam: MediaResult[], igdb: MediaResult[], normal: string): MediaResult[] {
    const steamKeys = new Set(steam.map((result) => queryKey(result.title)));
    const filling = igdb.filter((result) => !steamKeys.has(queryKey(result.title)));

    const exact = (result: MediaResult) => queryKey(result.title) === normal;
    const first = [...steam.filter(exact), ...filling.filter(exact)];
    const restSteam = steam.filter((result) => !exact(result));
    const restIGDB = filling.filter((result) => !exact(result));

    const room = 8 - first.length;
    const fromIGDB = Math.min(restIGDB.length, Math.max(room - restSteam.length, Math.min(3, room)));
    const fromSteam = Math.min(restSteam.length, room - fromIGDB);

    return [...first, ...restSteam.slice(0, fromSteam), ...restIGDB.slice(0, fromIGDB)];
}

// igdb: the admin has set up a Twitch application, so games also come from
// IGDB.
export function searchCatalogue(kind: 'movie' | 'game', query: string, igdb = false): MediaResult[] {
    const q = queryKey(query);
    if (q === '') {
        return [];
    }

    const found = (wanted: (entry: Entry) => boolean) => CATALOGUE.
        filter(wanted).
        filter((entry) => queryKey(entry.title).includes(q)).
        slice(0, 8).
        map(({ref, kind: what, title, year, label}) => ({ref, kind: what, title, year, label}));

    if (kind === 'movie') {
        return found((entry) => entry.kind !== 'game');
    }

    const steam = found((entry) => entry.ref.startsWith('steam:'));
    const fromIGDB = igdb ? found((entry) => entry.ref.startsWith('igdb:')) : [];

    return mergeGames(steam, fromIGDB, q);
}

export function detailsOf(ref: string): MediaInfo | null {
    const entry = CATALOGUE.find((candidate) => candidate.ref === ref);
    if (!entry) {
        return null;
    }

    const {label, hue, ...info} = entry;
    void label;
    void hue;

    return info;
}

const drawn: Record<string, string> = {};

// Posters are 2:3, backdrops 16:9, and a Steam game's thumb is its 231 × 87
// capsule; a Steam game's cover is its 600 × 900 box art. An IGDB game's thumb
// and cover are portrait box art too (IGDB's cover_small and cover_big), and
// its backdrop a wide screenshot.
export function artOf(ref: string, size: 'thumb' | 'cover' | 'backdrop'): string {
    const key = `${ref}|${size}`;
    if (drawn[key]) {
        return drawn[key];
    }

    const entry = CATALOGUE.find((candidate) => candidate.ref === ref);
    if (!entry || typeof document === 'undefined') {
        return '';
    }

    const game = entry.kind === 'game';
    const [w, h] = (() => {
        if (ref.startsWith('igdb:')) {
            if (size === 'backdrop') {
                return [889, 500];
            }

            return size === 'thumb' ? [90, 128] : [264, 352];
        }
        if (size === 'backdrop') {
            return game ? [460, 215] : [780, 439];
        }
        if (size === 'thumb') {
            return game ? [231, 87] : [92, 138];
        }

        return game ? [300, 450] : [342, 513];
    })();

    const canvas = document.createElement('canvas');
    canvas.width = w;
    canvas.height = h;
    const g = canvas.getContext('2d');
    if (!g) {
        return '';
    }

    const grad = g.createLinearGradient(0, 0, w, h);
    grad.addColorStop(0, `hsl(${entry.hue} 70% 32%)`);
    grad.addColorStop(1, `hsl(${(entry.hue + 40) % 360} 60% 10%)`);
    g.fillStyle = grad;
    g.fillRect(0, 0, w, h);

    // Something picture-like: a sun and a horizon, so the fade reads as art.
    g.fillStyle = `hsl(${(entry.hue + 20) % 360} 90% 65% / 0.55)`;
    g.beginPath();
    g.arc(w * 0.68, h * 0.38, Math.min(w, h) * 0.22, 0, Math.PI * 2);
    g.fill();
    g.fillStyle = 'rgba(0, 0, 0, 0.45)';
    g.fillRect(0, h * 0.7, w, h * 0.3);

    g.fillStyle = '#ffffff';
    g.font = `bold ${Math.round(Math.min(w, h) * 0.13)}px sans-serif`;
    g.textBaseline = 'bottom';
    g.fillText(entry.title.toUpperCase(), w * 0.07, h * 0.93, w * 0.86);

    drawn[key] = canvas.toDataURL('image/jpeg', 0.8);

    return drawn[key];
}
