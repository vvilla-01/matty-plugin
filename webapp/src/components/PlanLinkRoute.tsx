import React from 'react';

import {PLUGIN_ID} from '../manifest';
import {currentTeam, currentUserId, mattermostStore, replacePath} from '../mattermost';
import {useName} from '../name';
import {arriveAtPlan} from '../planLink';
import {getSnapshot, refreshSnapshot, snapshotAge} from '../store';

// Where a plan link lands (design/3.2.0.md 6.5, 6.7): /plug/<plugin id>/plan/
// <id>, the address a shared plan's post carries for the phone, the Threads
// list, pushes and email, which cannot draw the card. Registered in index.tsx
// with registerCustomRoute, in the main window only, as the clip link is
// (ClipLinkRoute.tsx).
//
//   - Not a plan's address: to Mattermost's front page.
//   - Nobody signed in: to sign in, coming back here afterwards.
//   - A plan no longer listed: said here, with the way on to Mattermost. The
//     schedule is open to everybody, so the snapshot answers it with no
//     request of its own beyond the open one every page makes.
//   - A plan listed: on to the reader's team, and the panel opened
//     there with that plan's Up next card in view (planLink.ts).

const LINK_PATH = `/plug/${PLUGIN_ID}/plan/`;
const LINK = new RegExp(`/plug/${PLUGIN_ID.replace(/\./g, '\\.')}/plan/([0-9a-f]{16})/?$`);

export const PLAN_LINK_GONE = 'That plan isn’t on the list any more.';

const teamPath = (): string => {
    const team = currentTeam();

    return team.name ? `/${team.name}` : '/';
};

export const PlanLinkRoute: React.FC<{pathname?: string}> = ({pathname}) => {
    const name = useName();
    const [gone, setGone] = React.useState(false);

    React.useEffect(() => {
        const match = LINK.exec(pathname ?? window.location.pathname);
        if (!match) {
            replacePath('/');

            return;
        }

        const id = match[1];
        if (currentUserId() === '') {
            replacePath(`/login?redirect_to=${encodeURIComponent(LINK_PATH + id)}`);

            return;
        }

        const snapshot = snapshotAge() === Infinity ? refreshSnapshot() : Promise.resolve(getSnapshot());

        snapshot.then((fresh) => {
            if (!(fresh.schedule || []).some((item) => item.id === id)) {
                setGone(true);

                return;
            }

            replacePath(teamPath());
            arriveAtPlan(id, mattermostStore());
        }).catch(() => setGone(true));

        // Once, as it mounts: it sends Mattermost on at once, so the next link
        // mounts it afresh.
    }, []);

    if (gone) {
        return (
            <div
                className='vntv vntv-cliplink vntv-planlink'
                role='status'
            >
                <p>{PLAN_LINK_GONE}</p>
                <button
                    type='button'
                    className='vntv-sheet-action'
                    onClick={() => replacePath(teamPath())}
                >{'Go to Mattermost'}</button>
            </div>
        );
    }

    return (
        <div
            className='vntv vntv-cliplink'
            role='status'
        >{`Opening the plan in ${name}…`}</div>
    );
};
