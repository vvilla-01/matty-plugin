import React from 'react';

import {installClipArrival, openClipFromLink} from '../clips';
import {getIdentity, refreshIdentity} from '../identity';
import {PLUGIN_ID} from '../manifest';
import {currentTeam, currentUserId, mattermostStore, replacePath} from '../mattermost';
import {useName} from '../name';

// Where a clip link lands (addendum 4.6 and 4.7): /plug/<plugin id>/clip/<id>,
// the address a shared clip's post carries for the phone, the Threads list,
// pushes and email, which cannot draw the card. Registered in index.tsx with
// registerCustomRoute, in the main window only; Mattermost draws it in its
// centre, in place of the channel.
//
// It does its work once, as it mounts, and sends Mattermost on:
//
//   - Not a clip's address: to Mattermost's front page.
//   - Nobody signed in (an email, a phone's browser): to sign in, coming back
//     here afterwards. The route is not a signed-in-only one, so this page
//     sends them itself.
//   - Somebody signed in: the Clips dialog set on the clip (or on the call to
//     link, for somebody not linked), then on to their team, which opens its
//     last channel; and the panel opened once there is a channel to
//     open it in (installClipArrival). The dialog lives at the root of the
//     app (ClipsRoot), so it is still there after this page has gone.
//
// The id is read from the address, not from anything Mattermost hands the
// page: it hands a plugin's route nothing but its theme. pathname is for the
// lab, which has no such address to be at.

const LINK_PATH = `/plug/${PLUGIN_ID}/clip/`;
const LINK = new RegExp(`/plug/${PLUGIN_ID.replace(/\./g, '\\.')}/clip/([0-9a-f]{16})/?$`);

export const ClipLinkRoute: React.FC<{pathname?: string}> = ({pathname}) => {
    const name = useName();

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

        // A "me" that is not here yet — somebody who signed in just now,
        // with no reload — is asked for first, so the dialog knows whether
        // to show the clip or the call to link. It shows either way once
        // "me" comes.
        const known = getIdentity() ? Promise.resolve() : refreshIdentity().then(() => undefined);
        known.then(() => {
            openClipFromLink(id);
            const team = currentTeam();
            replacePath(team.name ? `/${team.name}` : '/');
            const store = mattermostStore();
            if (store) {
                installClipArrival(store);
            }
        });

        // Once, as it mounts: it sends Mattermost on at once, so the next
        // link mounts it afresh.
    }, []);

    return (
        <div
            className='vntv vntv-cliplink'
            role='status'
        >{`Opening the clip in ${name}…`}</div>
    );
};
