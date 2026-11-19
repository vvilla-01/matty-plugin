import {closeClips, loadClip, openClips} from '../src/clips';

import {labSampleClipURL} from './mocks/client';
import {change, getWorld} from './world';
import type {World} from './world';

// The clip player (src/components/ClipTile.tsx), by script, for the
// screenshots: window.__villanetLab.clipPlayer. The same states as the Clips
// rows' chips (downloads on, Victor blocks downloads, Clip file), plus what
// only a script needs: the dialog opened afresh on one clip, a slower server
// so "Preparing…" can be seen, and clips' pictures made from frames of their
// file.

// What the viewer has of every clip is asked for again, so a clip already
// open takes up a change at once.
function refreshClips(): void {
    getWorld().clips.forEach((entry) => {
        void loadClip(entry.id).catch(() => undefined);
    });
}

// Clips' pictures made from frames of their file, as the server makes them,
// rather than the lab's drawn cards, which have a play button of their own
// painted in the middle, right where the loading ring goes. Each clip a
// different moment, so the list still tells them apart; seconds names the
// frame for any clip.
function frameThumbs(seconds?: Record<string, number>): Promise<boolean> {
    const ids = getWorld().clips.map((entry) => entry.id);
    const at = (id: string, index: number) => seconds?.[id] ?? (9 + (index * 13)) % 110;

    return new Promise<boolean>((resolve) => {
        const video = document.createElement('video');
        const pictures: Record<string, string> = {};
        let next = 0;
        video.muted = true;
        video.preload = 'auto';
        const seek = () => {
            if (next >= ids.length) {
                change((current) => {
                    current.clips = current.clips.map((entry) => (pictures[entry.id] ? {...entry, thumbUrl: pictures[entry.id]} : entry));
                });
                refreshClips();
                resolve(true);

                return;
            }
            video.currentTime = Math.min(at(ids[next], next), Math.max(0, video.duration - 0.5));
        };
        video.addEventListener('loadeddata', seek, {once: true});
        video.addEventListener('seeked', () => {
            const canvas = document.createElement('canvas');
            canvas.width = 640;
            canvas.height = 360;
            canvas.getContext('2d')?.drawImage(video, 0, 0, 640, 360);
            pictures[ids[next]] = canvas.toDataURL('image/jpeg', 0.85);
            next += 1;
            seek();
        });
        video.addEventListener('error', () => resolve(false), {once: true});
        video.src = labSampleClipURL();
    });
}

export const labClipPlayer = {
    // The dialog on one clip ("that jump" unless another is named), closed
    // first so the player starts afresh.
    open: (id = '3f9a0c2e7b1d4a55') => {
        closeClips();
        window.setTimeout(() => openClips(id), 50);
    },
    close: () => closeClips(),

    // How the clip's file comes: the Clip file row. Choose it before
    // opening the clip.
    file: (mode: World['clipFile']) => change((current) => {
        current.clipFile = mode;
    }),

    // The admin's Let people download clips: the Clips row's "downloads on".
    downloads: (on: boolean) => {
        change((current) => {
            current.downloadsAllowed = on;
        });
        refreshClips();
    },

    // Victor's own stream's switch, turned off or on behind an open
    // viewer's back: the next Download on a clip of his stream is refused.
    blockVictor: (on: boolean) => change((current) => {
        current.streamerNoDownloads = on;
    }),

    // How long the lab's server takes to answer; 180 ms is the lab's own.
    latency: (ms: number) => change((current) => {
        current.latencyMs = ms;
    }),

    frames: (seconds?: Record<string, number>) => frameThumbs(seconds),
};
