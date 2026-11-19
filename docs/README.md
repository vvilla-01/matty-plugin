# Matty Plugin

Matty Plugin is the companion to Matty, the Android app for
Mattermost: one panel beside chat for your own
[Broadcast Box](https://github.com/Glimesh/broadcast-box)'s live streams. Today that means watching
them without leaving Mattermost, clipping and sharing their best moments, planning streams and
sharing the plans, seeing who is in Mumble, awards and achievements, birthdays and, when an admin
switches them on, who is live on Twitch, people's Steam status and Hytale names, a soundboard for
whoever is in Mumble, and push notifications for Matty. Click its icon in the App Bar, unlock it once with your bearer token, and
everyone who is live is there; when a streamer is live, a red dot appears next to their name
everywhere in Mattermost.

Mattermost lists the plugin as **Matty** (Plugin Management, and **System Console > Plugins >
Matty**). What people on a server see it called is that server's own choice: the setting **Name**,
under [Name and artwork](install.md#name-and-artwork), which is Matty until an admin changes it. This
manual writes the sentences people read with the default, such as "Matty is locked."; on a server
with a name of its own, that name stands there instead.

## The chapters

1. [Getting started](install.md): What Matty Plugin does, installing it on a new server, its settings, giving it your own name and artwork, its bot and backing the settings up.
2. [Upgrading](upgrading.md): What to do when upgrading to each release, and what is new in each.
3. [Unlocking and streaming](unlocking.md): The lock screen and the bearer token, the red dot, stream notes, and hashtags and @names in titles.
4. [The panel](panel.md): The bar at the bottom, Live now, clips, the player, threads, and how the panel looks.
5. [Up next, Twitch, Steam and Hytale](up-next.md): Planning streams, movie and game lookups, and the optional Twitch, Steam and Hytale connections.
6. [Voice and birthdays](voice.md): Who is in Mumble, the soundboard, Mumble chat in a channel, and birthdays.
7. [Airtime and awards](awards.md): Airtime and medals, Top Snoopers, awards, biggest fans and achievements.
8. [Push notifications](push.md): Notifications on phones through a UnifiedPush app, without Matty staying connected: switching them on, what is sent where, your own push server, the status card, a lost phone, and when pushes do not arrive.
9. [Building and how it works](building.md): Building from source, signing a release, how the plugin works, and the API that Matty uses.
10. [Security and limitations](security.md): The security posture, signed releases, the app check, and notes and limitations.

Also in this repository: [LOCKDOWN.md](../LOCKDOWN.md), on putting Broadcast Box behind a reverse
proxy and a firewall, and [contract/README.md](../contract/README.md), the API both clients, the web
panel and Matty, code against.
