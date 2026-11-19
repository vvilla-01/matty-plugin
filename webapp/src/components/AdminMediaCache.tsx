import React from 'react';

import {fetchMediaCache, forgetMediaCache} from '../client';
import type {MediaCacheStatus} from '../client';

import {AdminCard, AdminHead, AdminPill, AdminStatus, useCardConfirm, useCardFocus} from './adminParts';

// Remembered titles, in System Console > Plugins > Matty > Movies and
// games: how much the server keeps of what it has looked up, so the same
// search or title is not looked up again for days, and a way to forget it.
//
// Counted only when this page asks; the panel never does. Forgetting acts at
// once, like the artwork and the preview pictures: nothing waits for the
// page's Save button. It edits no config value, so it takes no value prop and
// never calls onChange, and nothing here can leave Save dirty.

// Forgetting everything is a second press away, for a moment, as everything
// else on the page that cannot be undone is (twoClicks.ts, through
// useCardConfirm).

type Props = {
    id?: string;
    label?: string;
    disabled?: boolean;
};

function count(n: number, one: string, many: string): string {
    return `${n} ${n === 1 ? one : many}`;
}

// A sense of the size, not an exact figure: "79 KB", "2 MB", "1.4 MB".
function size(bytes: number): string {
    if (bytes < 1024) {
        return `${bytes} bytes`;
    }

    const kb = Math.round(bytes / 1024);
    if (kb < 1024) {
        return `${kb} KB`;
    }

    return `${Math.round(bytes / (1024 * 1024) * 10) / 10} MB`;
}

// The day the oldest entry was looked up, in the reader's own words for a
// date: "3 Sep", with the year once it is not this one.
function day(at: number, now: number): string {
    const sameYear = new Date(at).getFullYear() === new Date(now).getFullYear();

    return new Intl.DateTimeFormat(undefined, sameYear ? {day: 'numeric', month: 'short'} : {day: 'numeric', month: 'short', year: 'numeric'}).format(new Date(at));
}

function remembered(status: MediaCacheStatus): number {
    return status.titles + status.searches + status.missing;
}

// "Remembering 12 titles and 40 searches (79 KB of 2 MB), the oldest from
// 3 Sep." Titles a provider said do not exist are counted too, when there are
// any, so the size is never all that is shown for them.
function summary(status: MediaCacheStatus, now: number): string {
    if (remembered(status) === 0) {
        // "Yet" only while nothing was ever stored. Forgetting leaves the
        // sixteen documents behind, empty, so a few bytes means this is a
        // cache that was used and has been emptied.
        return status.bytes === 0 ? 'Nothing remembered yet.' : 'Nothing remembered.';
    }

    const what = status.missing > 0 ?
        `${count(status.titles, 'title', 'titles')}, ${count(status.searches, 'search', 'searches')} and ${count(status.missing, 'title not found', 'titles not found')}` :
        `${count(status.titles, 'title', 'titles')} and ${count(status.searches, 'search', 'searches')}`;
    const oldest = status.oldest > 0 ? `, the oldest from ${day(status.oldest, now)}` : '';

    return `Remembering ${what} (${size(status.bytes)} of ${size(status.limitBytes)})${oldest}.`;
}

// The pictures, on a line of their own: "Keeping 37 pictures on this server's
// disk (3.1 MB of 256 MB)." Nothing at all from a server that predates them,
// rather than a line that would guess.
function pictureLine(status: MediaCacheStatus): string {
    if (status.picturesKept === undefined) {
        return '';
    }
    if (!status.picturesKept) {
        return status.pictureProblem ?
            `Pictures are kept in memory only: ${status.pictureProblem}` :
            'Pictures are kept in memory only, because Mattermost’s file storage is not local.';
    }

    const pictures = status.pictures || 0;
    if (pictures === 0) {
        return 'No pictures kept on this server’s disk.';
    }

    return `Keeping ${count(pictures, 'picture', 'pictures')} on this server’s disk (${size(status.pictureBytes || 0)} of ${size(status.pictureLimitBytes || 0)}).`;
}

export const AdminMediaCache: React.FC<Props> = ({id, label, disabled}) => {
    const [status, setStatus] = React.useState<MediaCacheStatus | null>(null);
    const [loadError, setLoadError] = React.useState('');
    const [busy, setBusy] = React.useState(false);
    const [note, setNote] = React.useState('');
    const [error, setError] = React.useState('');
    const {statusRef, focus} = useCardFocus();
    const confirm = useCardConfirm(() => {
        setNote('');
        setError('');
    });

    // Only the newest answer is used: a reload after forgetting and one the
    // admin asked for can cross, and an older count landing last would bring
    // back what was just forgotten.
    const latest = React.useRef(0);
    const load = React.useCallback(() => {
        const ticket = ++latest.current;
        fetchMediaCache().then((next) => {
            if (ticket !== latest.current) {
                return;
            }
            setStatus(next);
            setLoadError('');
        }).catch((reason: unknown) => {
            if (ticket !== latest.current) {
                return;
            }
            setLoadError(reason instanceof Error ? reason.message : String(reason));
        });
    }, []);

    React.useEffect(() => {
        load();

        // Anything still on its way arrives to nobody.
        return () => {
            latest.current++;
        };
    }, [load]);

    const forget = (event: React.MouseEvent<HTMLButtonElement>) => {
        if (!confirm.press('forget', 'Click again to forget every remembered title and picture. Anything wanted again is simply looked up again.', event)) {
            return;
        }

        setBusy(true);
        forgetMediaCache().then((result) => {
            const pictures = result.picturesForgotten || 0;
            if (result.forgotten === 0 && pictures === 0) {
                setNote('There was nothing to forget.');
            } else if (pictures === 0) {
                setNote(`Forgot ${count(result.forgotten, 'remembered entry', 'remembered entries')}.`);
            } else if (result.forgotten === 0) {
                setNote(`Forgot ${count(pictures, 'picture', 'pictures')}.`);
            } else {
                setNote(`Forgot ${count(result.forgotten, 'remembered entry', 'remembered entries')} and ${count(pictures, 'picture', 'pictures')}.`);
            }
        }).catch((reason: unknown) => {
            setError(reason instanceof Error ? reason.message : String(reason));
        }).finally(() => {
            setBusy(false);

            // Either way the counts are read again: a failure can still have
            // emptied some of it.
            load();
        });
    };


    // Nothing counted is nothing to forget: two presses would only report
    // "There was nothing to forget.". Refresh first if another server may
    // have added some.
    const empty = status !== null && remembered(status) === 0 && !status.pictures;
    const pictures = status ? pictureLine(status) : '';

    // A count that could not be read: with nothing counted yet the head says
    // so, and the line under it only why; over an older count, the line says
    // both.
    let loadLine = loadError;
    if (loadError !== '' && status) {
        loadLine = `The remembered titles could not be counted: ${loadError}`;
    }

    return (
        <AdminCard
            id={id}
            label={label}
            focus={focus}
        >
            {/* A count that could not be read leaves the last one standing,
                or says so, with the reason in the error line below. */}
            <AdminHead instant={true}>
                {(() => {
                    if (status) {
                        return summary(status, Date.now());
                    }

                    return loadError === '' ? 'Counting…' : 'The remembered titles could not be counted.';
                })()}
            </AdminHead>
            {pictures === '' ? null : <p className='vntv-admin-line vntv-admin-quiet'>{pictures}</p>}

            <div className='vntv-admin-actions'>
                <AdminPill
                    off={disabled || empty}
                    waiting={busy}
                    working={busy}
                    workingWords='Forgetting…'
                    confirms={true}
                    armed={confirm.armed === 'forget'}
                    onPress={forget}
                >{'Forget remembered titles'}</AdminPill>
                <AdminPill
                    waiting={busy}
                    onPress={() => {
                        // A fresh look: what was said about the last
                        // press no longer describes what is about to show.
                        setNote('');
                        setError('');
                        confirm.disarm();
                        load();
                    }}
                >{'Refresh'}</AdminPill>
            </div>

            <AdminStatus
                ref={statusRef}
                note={confirm.hint || note}
                error={error || loadLine}
            />
        </AdminCard>
    );
};
