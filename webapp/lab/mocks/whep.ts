import {getWorld, labAccessOf} from '../world';

import {labRefusal} from './client';

// A stand-in for the WebRTC player.
//
// It paints a canvas and hands over canvas.captureStream(), which is a real
// MediaStream — so muting, the volume control and the audio-exclusivity rule
// behave exactly as they do against a real broadcast rather than against a
// still image that cannot be played or paused.

// How long to take before handing the picture over.
//
// Zero by default, because an instant connection is what the lab wants for
// everything else. It exists because the channel-change effect is the one thing
// in the panel that is only visible WHILE a stream is being found: against an
// instant mock it renders for less than a frame, which is indistinguishable
// from not rendering at all, and that is precisely the difference the lab is
// supposed to be able to show.
let tuneDelay = 0;

export const setTuneDelay = (ms: number): void => {
    tuneDelay = ms;
};

export const getTuneDelay = (): number => tuneDelay;

// What the lab counts, on window.__villanetLab so it can be read from the
// console as well as from the lab's own column: every WHEP exchange a real
// player would have made (plays), every one hung up (stops), how many are open
// right now (live), and the preview-still peeks, which are counted apart so
// that "no new connection" can be read straight off plays. stills counts the
// preview pictures uploaded, which is how stills taken from the small player
// can be seen arriving. clips counts every request to a panel clip route
// (/api/v1/clips*), which must stay at nought for anybody not linked, and
// cards the requests among them for shared clips' cards, one per 25 ids.
export type LabCounters = {plays: number; stops: number; live: number; peeks: number; stills: number; clips: number; cards: number; downloads: number};

type LabWindow = Window & {__villanetLab?: LabCounters};

export function labCounters(): LabCounters {
    const win = window as LabWindow;
    if (!win.__villanetLab) {
        win.__villanetLab = {plays: 0, stops: 0, live: 0, peeks: 0, stills: 0, clips: 0, cards: 0, downloads: 0};
    }

    return win.__villanetLab;
}

export function resetLabCounters(): void {
    const counters = labCounters();
    counters.plays = 0;
    counters.stops = 0;
    counters.peeks = 0;
    counters.stills = 0;
    counters.clips = 0;
    counters.cards = 0;
    counters.downloads = 0;
}

// The players with a stream open, so the lab can make them fail the way a
// real connection does when ICE gives up: frozen on its last frame, with the
// failure reported through the same callback.
const open = new Set<WhepPlayer>();

// Who the lab's server has heard is watching, as the real one hears it: from
// the exchange that starts a session, and nothing else. Each stream's latest
// tune-in, and the moment the server forgot everybody (the Capture row's
// "server forgot", a restart), on one counter so that the two can never tie.
// Only the clip routes ask (mocks/client.ts createDraft).
let heardTick = 0;
let forgottenAt = 0;
const tunedIn = new Map<string, number>();

export function forgetViewers(): void {
    forgottenAt = ++heardTick;
}

export function heardWatching(handle: string): boolean {
    return (tunedIn.get(handle) || 0) > forgottenAt;
}

export function failOpenStreams(): void {
    Array.from(open).forEach((player) => player.failNow('Lost the connection to this stream.'));
}

export class WhepPlayer {
    private timer = 0;
    private stream: MediaStream | null = null;
    private counted = false;
    private onFailure: ((reason: string) => void) | undefined;

    // Which play() is the current one, as in the real player: Retry during a
    // slow tune stops the first and starts a second on the same element, and
    // the first must not hand its picture over when its wait ends.
    private generation = 0;

    async play(handle: string, video: HTMLVideoElement, onFailure?: (reason: string) => void, peekLease?: string): Promise<void> {
        this.stop();
        const generation = ++this.generation;

        // The server's front gate: a locked reader is refused, and a peek
        // (taken only for a preview still) needs a link. The page is told,
        // as the real client tells it, and reads "me" again.
        const access = labAccessOf(getWorld());
        if (access === 'locked' || (peekLease && access !== 'linked')) {
            const code = access === 'locked' ? 'locked' : 'watch_only';
            labRefusal(code);
            onFailure?.(code === 'locked' ? `${getWorld().me.name} is locked. Unlock it with your bearer token.` : 'You are let in to watch only.');

            return;
        }

        const counters = labCounters();
        if (peekLease) {
            counters.peeks++;
        } else {
            counters.plays++;
            counters.live++;
            this.counted = true;
            this.onFailure = onFailure;
            open.add(this);
            tunedIn.set(handle, ++heardTick);
        }

        const canvas = document.createElement('canvas');
        canvas.width = 640;
        canvas.height = 360;

        const context = canvas.getContext('2d');
        if (!context) {
            if (onFailure) {
                onFailure('This browser would not give the lab a canvas.');
            }

            return;
        }

        // A colour of its own for each stream, so preview stills taken from
        // these pictures can be told apart at a glance.
        let hash = 0;
        for (const char of handle) {
            hash = ((hash * 31) + char.charCodeAt(0)) >>> 0;
        }
        const hue = hash % 360;

        const started = Date.now();
        const draw = () => {
            const seconds = (Date.now() - started) / 1000;

            const sky = context.createLinearGradient(0, 0, 0, canvas.height);
            sky.addColorStop(0, `hsl(${hue}, 55%, 22%)`);
            sky.addColorStop(0.62, `hsl(${(hue + 40) % 360}, 60%, 38%)`);
            sky.addColorStop(0.62, `hsl(${(hue + 180) % 360}, 30%, 14%)`);
            sky.addColorStop(1, '#050505');
            context.fillStyle = sky;
            context.fillRect(0, 0, canvas.width, canvas.height);

            context.fillStyle = `hsla(${(hue + 60) % 360}, 90%, 70%, 0.85)`;
            context.beginPath();
            context.arc(canvas.width * 0.72, canvas.height * 0.44, 46, 0, Math.PI * 2);
            context.fill();

            // Something that moves, so a frozen tile is obvious.
            const x = (canvas.width / 2) + (Math.cos(seconds) * 140);
            const y = (canvas.height / 2) + (Math.sin(seconds * 1.3) * 70);
            context.fillStyle = '#f2a900';
            context.beginPath();
            context.arc(x, y, 26, 0, Math.PI * 2);
            context.fill();

            context.fillStyle = '#ffffff';
            context.font = '600 20px system-ui, sans-serif';
            context.fillText(handle, 18, 34);
            context.font = '14px system-ui, sans-serif';
            context.fillText(`${seconds.toFixed(1)}s`, 18, 56);
        };

        draw();
        this.timer = window.setInterval(draw, 60);

        if (tuneDelay > 0) {
            await new Promise((resolve) => window.setTimeout(resolve, tuneDelay));

            // Stopped while it was tuning, which is what happens when somebody
            // changes their mind, or stopped and started again by Retry. The
            // canvas timer is already cleared; handing this stream over now
            // would attach a frozen picture to the element.
            if (generation !== this.generation) {
                return;
            }
        }

        const stream = canvas.captureStream(25);
        this.stream = stream;
        video.srcObject = stream;
        await video.play().catch(() => undefined);
    }

    // The picture freezes, as it does when a connection is lost, and the
    // player says so. The stream stays open until it is stopped, like a real
    // peer connection in the failed state.
    failNow(reason: string): void {
        if (this.timer) {
            window.clearInterval(this.timer);
            this.timer = 0;
        }

        this.onFailure?.(reason);
    }

    stop(): void {
        this.generation++;

        if (this.counted) {
            const counters = labCounters();
            counters.stops++;
            counters.live = Math.max(0, counters.live - 1);
            this.counted = false;
            this.onFailure = undefined;
            open.delete(this);
        }

        if (this.timer) {
            window.clearInterval(this.timer);
            this.timer = 0;
        }

        if (this.stream) {
            this.stream.getTracks().forEach((track) => track.stop());
            this.stream = null;
        }
    }
}
