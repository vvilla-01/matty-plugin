import React from 'react';
import type {Store} from 'redux';

import {PLUGIN_ID} from './manifest';
import {APP_BAR_ICON} from './icon';
import {nameNow, onNameChange, useName} from './name';
import {injectCodeStyles, injectStyles, removeStyles} from './styles';
import {refreshSnapshot, setPushedSnapshot} from './store';
import {installBoards} from './boards';
import {followSignIn, refreshIdentity, setIdentity} from './identity';
import {installReconciler, resetWatching, setSidebarOpener} from './watching';
import {installSessions, stopAllSessions} from './sessions';
import {getAway, installAway, windowKind} from './away';
import {userSettings} from './userSettings';
import type {UserSettings} from './userSettings';
import {setMattermostStore, setPanelCloser} from './mattermost';
import {answerPopout, forgetTagSearch} from './tagSearch';
import type {PopoutListeners} from './tagSearch';
import {ADMIN_SETTINGS} from './adminSettings';
import {PanelGate} from './components/PanelGate';
import {installAccess, signedOut} from './access';
import {openerPress} from './lockPending';
import {PostAwardBadges} from './components/AwardBadges';
import {PopoverLive} from './components/PopoverLive';
import {PopoverTakebacks} from './components/PopoverTakebacks';
import {MiniPlayerHost, MiniPlayerRoot} from './components/MiniPlayer';
import {ClipsRoot} from './components/ClipsRoot';
import {ClipLinkRoute} from './components/ClipLinkRoute';
import {ClipPostCard} from './components/ClipPostCard';
import {PlanLinkRoute} from './components/PlanLinkRoute';
import {PlanPostCard} from './components/PlanPostCard';
import {resetClips} from './clips';
import {CLIP_POST_TYPE} from './clipLogic';
import {forgetOwnAnswers} from './ownAnswers';
import {forgetPlanLink} from './planLink';
import {PLAN_POST_TYPE} from './planLogic';
import {forgetSoundboard, heardSound} from './soundboard';
import {accountsEventHeard, clearAccounts, followAccountsAccess} from './accounts';
import {SidebarTitle} from './components/SidebarTitle';
import {BrandIcon} from './components/BrandIcon';
import {installTwitch} from './twitch';
import {ME_EVENT, meHeard} from './me';

const WS_EVENT = `custom_${PLUGIN_ID}_streams`;

// Sent to each Mattermost account matched in the voice roster as somebody's
// sound starts and as it ends, {"busyUntil": ms}, naming no sound and no
// person (design/3.2.0.md 2.3, 7.10): the soundboard greys its tiles while
// something plays.
const SOUND_EVENT = `custom_${PLUGIN_ID}_sound`;

// Sent to everybody, with nothing in it, when anybody's Steam status or
// Hytale name as GET /api/v1/accounts has them changed: read them again, if
// you may watch (design/3.4.0.md 2.9). A broadcast because the server cannot
// send to a set of people; a locked reader who asked would be refused.
const ACCOUNTS_EVENT = `custom_${PLUGIN_ID}_accounts`;

type RHSRegistration = {
    id: string;
    showRHSPlugin: unknown;
    hideRHSPlugin: unknown;
    toggleRHSPlugin: unknown;
};

// Only the handful of registry methods this plugin uses, as Mattermost 11.11
// has them (webapp/channels/src/plugins/registry.ts). Two are marked INTERNAL
// there, subject to change without notice: registerAppBarComponent and
// registerGlobalComponent. Those stay optional, so a Mattermost that drops one
// loses that entry point or that mount and nothing else.
type Registry = {
    registerRightHandSidebarComponent: (component: React.ComponentType, title: React.ReactNode) => RHSRegistration;
    registerChannelHeaderButtonAction: (
        icon: React.ReactNode,
        action: () => void,
        dropdownText: React.ReactNode,
        tooltipText: React.ReactNode,
    ) => string;
    registerPopoverUserAttributesComponent: (component: React.ComponentType<any>) => string;

    // Rendered in every profile popover, one's own included, after
    // Mattermost's own row (design/3.2.0.md M16), and handed {user, hide,
    // status}: where your own takebacks go.
    registerPopoverUserActionsComponent: (component: React.ComponentType<any>) => string;

    // Renders in the header of every post, beside the name and the timestamp.
    registerPostHeaderComponent: (component: React.ComponentType<any>) => string;
    registerWebSocketEventHandler: (event: string, handler: (message: any) => void) => void;
    unregisterWebSocketEventHandler: (event: string) => void;

    // After every WebSocket reconnect, and before a message is posted.
    registerReconnectHandler: (handler: () => void) => void;
    unregisterReconnectHandler: () => void;
    registerMessageWillBePostedHook: (hook: (post: {message?: unknown}) => unknown) => unknown;
    registerAppBarComponent?: (iconUrl: string, action: () => void, tooltipText: React.ReactNode) => unknown;

    registerAdminConsoleCustomSetting: (
        key: string,
        component: React.ComponentType<any>,
        options?: {showTitle: boolean},
    ) => void;

    // Called each time this plugin's panel is popped out into a window of its
    // own, with a way to talk to that window. Mattermost hands over only the
    // listeners it has (Partial<PopoutListeners>), which answerPopout allows.
    registerRHSPluginPopoutListener: (
        pluginId: string,
        onPopoutOpened: (teamName: string, channelName: string | undefined, listeners: PopoutListeners) => void,
    ) => void;

    // Where the small player is mounted. Global renders at the root of the
    // app beside the side panel, survives product switches, and never renders
    // in a popped-out window; it is marked internal, so Root, which renders in
    // the channels view and in every popped-out window, is the fallback.
    registerGlobalComponent?: (component: React.ComponentType) => string;
    registerRootComponent: (component: React.ComponentType) => string;

    // Settings › Plugin preferences.
    registerUserSettings: (settings: UserSettings) => void;

    // A component drawn in place of the message of every post of one custom
    // type, wherever Mattermost shows the post: the channel, threads, search,
    // pinned and saved messages and permalink previews. Handed {post,
    // compactDisplay, isRHS, theme}. For a shared clip's card.
    registerPostTypeComponent: (type: string, component: React.ComponentType<any>) => string;

    // A page of the plugin's own at /plug/<plugin id>/<route>, and anything
    // under it, drawn in Mattermost's centre: where a clip link lands. Not a
    // signed-in-only route, so the page sends a signed-out visitor to sign in.
    registerCustomRoute: (route: string, component: React.ComponentType<any>) => string;
};

// What this server calls the plugin (name.ts), as words in Mattermost's own
// furniture: the App Bar's tooltip and the channel header button's text and
// tooltip. A component, so it follows the name without registering again.
const NameText: React.FC = () => <>{useName()}</>;

// The same picture as the App Bar button, from the same address, so an icon
// uploaded in System Console changes both. Mattermost draws this one wherever
// there is no App Bar: on the desktop only when the App Bar is turned off
// (with it on, the channel-header button of a plugin that has an App Bar icon
// is left out), and always in the narrow header of a phone.
//
// Named, not decorative: the button it sits in has no label of its own in
// the narrow header, and none anywhere once its tooltip is a component
// (Mattermost makes a button's label only from a tooltip that is a string).
// Its alt is the name, so the name is what is read.
const HeaderIcon: React.FC = () => (
    <span style={{display: 'inline-flex', alignItems: 'center'}}>
        <BrandIcon
            size={18}
            alt={useName()}
        />
    </span>
);

export default class Plugin {
    private websocketRegistered = false;
    private unloaded = false;

    // What initialize() started outside React, each with the function that
    // undoes it, for uninitialize() to call in order.
    private uninstallSessions: (() => void) | null = null;
    private uninstallReconciler: (() => void) | null = null;
    private uninstallAway: (() => void) | null = null;
    private unfollowSignIn: (() => void) | null = null;
    private uninstallAccess: (() => void) | null = null;
    private uninstallTwitch: (() => void) | null = null;
    private uninstallBoards: (() => void) | null = null;
    private unfollowAccounts: (() => void) | null = null;
    private unfollowName: (() => void) | null = null;

    public initialize(registry: Registry, store: Store): void {
        this.unloaded = false;
        injectStyles();

        // The streams' connections, which outlive the panel (sessions.ts),
        // and what keeps them honest when the panel is not mounted to do it.
        // Both before anything is registered, so they hear about every change
        // of the watch list and every snapshot before any component does.
        this.uninstallSessions = installSessions();
        this.uninstallReconciler = installReconciler(() => getAway().phase !== 'idle');

        // Which handles are Twitch's, followed on every snapshot, and the
        // Twitch theatre closed with its stream (twitch.ts).
        this.uninstallTwitch = installTwitch();

        // The boards and everybody's record, which are not in the snapshot:
        // read once the first snapshot says there are any, and again when it
        // says they have changed (boards.ts).
        this.uninstallBoards = installBoards();

        // Everybody's Steam status and Hytale names, read as soon as "me"
        // says the reader may watch, and emptied as soon as it says not
        // (accounts.ts).
        this.unfollowAccounts = followAccountsAccess();

        // So that a press of the side panel's divider while the panel is
        // widening it can put back the width Mattermost keeps for everything
        // else in the side panel (rhsWidth.ts), and so the hashtag search can
        // name people and channels as Mattermost does (tagSearch.ts).
        setMattermostStore(store);

        // The airtime awards are rendered from the snapshot wherever a name
        // appears, so the snapshot has to arrive whether or not anybody opens
        // the panel. The WebSocket only pushes when something CHANGES, so a
        // page loaded during a quiet spell would otherwise show no awards until
        // the next stream started.
        const firstSnapshot = refreshSnapshot().catch(() => {
            // Not configured, or no session yet; the panel will try again.
            return null;
        });

        // Opt-in, so it costs one request and normally changes nothing.
        //
        // The unloaded check is what stops this outliving the plugin: an
        // update tears the webapp down while this request is still in flight,
        // and injecting afterwards would leave global CSS behind with nothing
        // left to remove it.
        //
        // Through refreshIdentity, which keeps what it reads for everything
        // outside the panel — the trophy in the sidebar header highlights the
        // reader's own row, and it is rendered outside the panel, so it cannot
        // be told by it — and which every other ask for "me" while this one is
        // on its way shares: a channel of shared clips' cards mounting as the
        // page loads asks for "me" too (ClipPostCard), and gets this answer
        // rather than sending a second request. It never rejects; null is "me"
        // could not be read (not configured, or no session yet).
        refreshIdentity().then((me) => {
            if (this.unloaded || !me) {
                return;
            }

            if (me.styleCodeBlocks) {
                injectCodeStyles();
            }

            // Opening the panel by itself, if an admin has asked for that.
            //
            // Decided ONCE, here, rather than watched for: this runs when the
            // page loads, so somebody who closes the panel is left alone for
            // the rest of their session instead of having it reopened under
            // them every time a poll finds somebody live.
            //
            // For everybody, locked people included, straight onto the lock
            // (Victor's answer of 2026-10-05). Opened by itself, so the lock
            // never puts its bearer token bar in focus: somebody typing a
            // message would type it into the token field.
            if (me.openPanel === 'never') {
                return;
            }

            firstSnapshot.then((snapshot) => {
                if (this.unloaded) {
                    return;
                }

                // "live" is the one worth having: the panel appears when there
                // is something to watch and stays out of the way when there is
                // not, which is most evenings.
                if (me.openPanel === 'live' && (snapshot?.streams || []).length === 0) {
                    return;
                }

                store.dispatch(sidebar.showRHSPlugin as never);
            }).catch(() => {
                // No snapshot, nothing to open the panel for.
            });
        });

        // Somebody signing in after the page loaded. Mattermost starts
        // plugins on the sign-in page and does not start them again once
        // somebody has signed in with a password, so both requests above were
        // refused and nothing would ask again until the panel was opened: no
        // awards beside names, no clapper, no shared clip a
        // linked reader could watch. So "me" and the snapshot are asked for
        // again the moment the store says who has signed in (identity.ts).
        // The panel is not opened for them: that was decided as the page
        // loaded.
        //
        // Signing out, which does not reload the page either, forgets the
        // person: the Clips dialog closed, their clips and drafts dropped,
        // what a hashtag search was told in their name forgotten, and "me"
        // unknown again, as at uninitialize, so whoever signs in next
        // sees nothing of theirs and nothing is asked for in their name
        // until their own "me" has come.
        this.unfollowSignIn = followSignIn(store, () => {
            refreshIdentity().then((me) => {
                if (me && !this.unloaded && me.styleCodeBlocks) {
                    injectCodeStyles();
                }
            });
            refreshSnapshot().catch(() => {
                // The panel, or the next push, will bring it.
            });
        }, () => {
            // Nothing of theirs carries over to whoever signs in next: no
            // tiles, no small player, nothing waiting for their lock.
            signedOut();
            resetClips();
            forgetTagSearch();
            forgetOwnAnswers();
            forgetSoundboard();
            forgetPlanLink();
            clearAccounts();
            setIdentity(null);
        });

        // What the side panel shows for the panel: the lock screen until the
        // reader has unlocked it with their own bearer token or been let in
        // by a system admin, and the panel after (PanelGate.tsx).
        const sidebar = registry.registerRightHandSidebarComponent(PanelGate, <SidebarTitle/>);

        // A press of the App Bar or header button that opens the panel is the
        // person's own opening, which may put the lock's bar in focus, and a
        // plain one, which lets go of anything waiting for the lock.
        const toggle = openerPress(() => store.dispatch(sidebar.toggleRHSPlugin as never));

        // Following the reader's access: the "me" event, reconnects,
        // refusals, the re-read while something plays, and a bearer token
        // kept out of the message box (access.ts).
        this.uninstallAccess = installAccess(registry, ME_EVENT, meHeard);

        // The small player that keeps the stream on screen while a thread or a
        // search has the panel's place: the main window only. Popped-out
        // channels have a side panel of their own, where the panel comes back
        // just the same, but no small player.
        const miniHost = windowKind() === 'main';
        if (miniHost) {
            if (typeof registry.registerGlobalComponent === 'function') {
                registry.registerGlobalComponent(MiniPlayerHost);
            } else {
                registry.registerRootComponent(MiniPlayerRoot);
            }
        }

        // The Clips dialog, at the root of the app, so it outlives the panel
        // and opens from anywhere (ClipsRoot, addendum 4.8). In the main
        // window as a Global component, which renders on every route, or as a
        // Root one where that is missing; in a popped-out window as a Root
        // one, the only kind that renders there. ClipsRoot draws one dialog
        // however many of it are mounted.
        if (windowKind() === 'main' && typeof registry.registerGlobalComponent === 'function') {
            registry.registerGlobalComponent(ClipsRoot);
        } else {
            registry.registerRootComponent(ClipsRoot);
        }

        // A shared clip's post, drawn as its card wherever Mattermost shows the
        // post, in every kind of window (ClipPostCard, addendum 4.9).
        registry.registerPostTypeComponent(CLIP_POST_TYPE, ClipPostCard);

        // A shared plan's post, the same way (design/3.2.0.md 6.5).
        registry.registerPostTypeComponent(PLAN_POST_TYPE, PlanPostCard);

        // Where the link in a shared clip's post lands, /plug/<plugin id>/clip/
        // <id>: the main window only, which is where it is opened from a phone's
        // browser or an email, and where the dialog and the panel it opens
        // live (ClipLinkRoute, addendum 4.7).
        if (windowKind() === 'main') {
            registry.registerCustomRoute('clip', ClipLinkRoute);

            // And a plan link's, /plug/<plugin id>/plan/<id> (6.5).
            registry.registerCustomRoute('plan', PlanLinkRoute);
        }

        // Telling the panel closed from the panel covered, and bringing it back
        // after a reply or when what covered it is closed. Closing it hangs up
        // every stream at once; a thread or a search taking its place leaves
        // them playing. See away.ts.
        this.uninstallAway = installAway({
            store,
            sidebarId: sidebar.id,
            show: () => store.dispatch(sidebar.showRHSPlugin as never),
            miniHost,
        });

        // The two per-person settings for all of that, in Settings › Plugin
        // preferences, under the server's name for the plugin: registered
        // again when the name changes, since Mattermost takes the tab's name
        // only as a string, and replaces the entry in place.
        const registerSettings = () => registry.registerUserSettings(userSettings(nameNow(), APP_BAR_ICON));
        registerSettings();
        this.unfollowName = onNameChange(registerSettings);

        // The profile popover opens the panel rather than toggling it, so
        // clicking "Watch" never closes what the viewer just asked for.
        setSidebarOpener(() => store.dispatch(sidebar.showRHSPlugin as never));

        // A message opened from the hashtag search on a phone-sized screen
        // closes the panel first, as Mattermost's own Jump does (mattermost.ts).
        setPanelCloser(() => store.dispatch(sidebar.hideRHSPlugin as never));

        // The App Bar button: the server's icon, and its name as the tooltip.
        if (typeof registry.registerAppBarComponent === 'function') {
            registry.registerAppBarComponent(APP_BAR_ICON, toggle, <NameText/>);
        }

        // Registered as well as the App Bar icon so there is always a visible
        // entry point, including on mobile and when the App Bar is hidden.
        registry.registerChannelHeaderButtonAction(
            <HeaderIcon/>,
            toggle,
            <NameText/>,
            <NameText/>,
        );

        registry.registerPopoverUserAttributesComponent(PopoverLive);

        // Taking things back from your own profile (design/3.2.0.md 4.6): not
        // being named as a biggest fan, and your own sounds. Open to anybody
        // signed in, linked or not; it draws nothing in anybody else's.
        registry.registerPopoverUserActionsComponent(PopoverTakebacks);

        // A popped-out panel has no search bar of its own, so its "Open in
        // Mattermost search" asks this window to open one. See tagSearch.ts.
        registry.registerRHSPluginPopoutListener(PLUGIN_ID, (_teamName, _channelName, listeners) => answerPopout(listeners));

        // Awards beside the name on a post. Additive only — it renders next to
        // the timestamp and touches nothing about how the message itself is
        // formatted or rendered.
        registry.registerPostHeaderComponent(PostAwardBadges);

        // The plugin's own controls on System Console → Plugins → Matty,
        // not in the panel: the airtime tools, the birthday list, the bot's
        // picture, the artwork and the rest. The list, and why each is where
        // it is, is in adminSettings.ts, which the lab renders too.
        for (const setting of ADMIN_SETTINGS) {
            registry.registerAdminConsoleCustomSetting(setting.key, setting.component, {showTitle: setting.showTitle});
        }

        registry.registerWebSocketEventHandler(WS_EVENT, (message: any) => {
            const payload = message?.data?.payload;
            if (typeof payload !== 'string') {
                return;
            }

            // With the tag it came under, so the next read can say which
            // snapshot is in hand and be told "that one still".
            try {
                setPushedSnapshot(JSON.parse(payload), message?.data?.tag);
            } catch {
                // A malformed push is ignored; the periodic refresh recovers.
            }
        });


        // Somebody's sound starting or ending (soundboard.ts).
        registry.registerWebSocketEventHandler(SOUND_EVENT, (message: any) => {
            heardSound(message?.data);
        });

        // Somebody's Steam status or Hytale name changed: read again, a
        // second after the first of a burst, if the reader may watch.
        registry.registerWebSocketEventHandler(ACCOUNTS_EVENT, () => {
            accountsEventHeard();
        });

        this.websocketRegistered = true;
    }

    public uninitialize(registry?: Registry): void {
        this.unloaded = true;

        // Nothing of the plugin may go on running once it is switched off: no
        // listener on Mattermost's store, no timer, and above all no stream
        // still connected with nothing left to show it or hang it up.
        this.uninstallAway?.();
        this.uninstallAway = null;
        this.unfollowSignIn?.();
        this.unfollowSignIn = null;
        this.uninstallAccess?.();
        this.uninstallAccess = null;
        this.uninstallReconciler?.();
        this.uninstallReconciler = null;
        stopAllSessions('unload');
        this.uninstallSessions?.();
        this.uninstallSessions = null;
        resetWatching();

        if (this.websocketRegistered && registry?.unregisterWebSocketEventHandler) {
            registry.unregisterWebSocketEventHandler(WS_EVENT);
            registry.unregisterWebSocketEventHandler(SOUND_EVENT);
            registry.unregisterWebSocketEventHandler(ACCOUNTS_EVENT);
            this.websocketRegistered = false;
        }
        this.uninstallTwitch?.();
        this.uninstallTwitch = null;
        this.uninstallBoards?.();
        this.uninstallBoards = null;
        this.unfollowAccounts?.();
        this.unfollowAccounts = null;
        this.unfollowName?.();
        this.unfollowName = null;

        setSidebarOpener(null);
        setPanelCloser(null);

        // The Clips dialog closed and everything fetched for it dropped —
        // the shared clips' cards included, and a clip link's wait for a
        // channel stopped — before the identity goes, so nothing is asked for
        // on the way out.
        resetClips();
        forgetTagSearch();
        forgetOwnAnswers();
        forgetSoundboard();
        forgetPlanLink();
        clearAccounts();
        setIdentity(null);
        setMattermostStore(null);
        removeStyles();
    }
}

declare global {
    interface Window {
        registerPlugin(pluginId: string, plugin: Plugin): void;
    }
}

window.registerPlugin(PLUGIN_ID, new Plugin());
