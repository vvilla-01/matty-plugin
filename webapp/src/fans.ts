import type {Boards, FanView} from './types';

// The biggest fan (design/3.2.0.md 4): per linked streamer, the linked viewer
// who has spent the most time watching them in the panel, all time, once it
// is an hour. Counted only while the cards show who is watching (Victor's
// answer to Q1), so nothing is learned about who watches whom that the cards
// were not already showing.
//
// Read out of the boards (boards.ts), where the fans are listed. Absent
// altogether while Biggest fans are off. Nobody can opt out from 3.2.1
// (design/3.2.1.md 2): the admin's switch is the only way to stop it. (Whose
// fan somebody is, the other way round, is a row of their record, which the
// server decides.)

// A streamer's biggest fan, if they have one.
export function fanOf(boards: Boards, streamerId?: string): FanView | null {
    if (!streamerId || !boards.fansOn) {
        return null;
    }

    return (boards.fans || []).find((row) => row.streamerId === streamerId) || null;
}

// "1 hour", "41 hours".
export function hoursInWords(hours: number): string {
    return `${hours} ${hours === 1 ? 'hour' : 'hours'}`;
}

export const FAN_TITLE = 'Biggest fan';

// The award's name, as its badge says it and as the fan's profile row heads
// it: "Biggest fan of Dave".
export function fanTitleOf(streamer: string): string {
    return `Biggest fan of ${streamer}`;
}

// What the counting is, said where the fans are listed: only while faces are
// on. With the server's name for the plugin (name.ts).
export const fansHint = (name: string): string => `Who has watched each streamer most in ${name}, counted while the cards show who is watching.`;
