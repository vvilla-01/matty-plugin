// formatUptime turns an RFC3339 stream start into a short, human duration.
export function formatUptime(iso?: string): string {
    if (!iso) {
        return '';
    }

    const started = Date.parse(iso);
    if (Number.isNaN(started)) {
        return '';
    }

    let seconds = Math.max(0, Math.floor((Date.now() - started) / 1000));

    const days = Math.floor(seconds / 86400);
    seconds -= days * 86400;
    const hours = Math.floor(seconds / 3600);
    seconds -= hours * 3600;
    const minutes = Math.floor(seconds / 60);

    if (days > 0) {
        return `${days}d ${hours}h`;
    }
    if (hours > 0) {
        return `${hours}h ${minutes}m`;
    }

    return `${minutes}m`;
}

// formatDuration renders accumulated airtime. Hours are the unit people compare
// on, so days are shown alongside them rather than replacing them.
export function formatDuration(seconds: number): string {
    const totalMinutes = Math.floor(Math.max(0, seconds) / 60);
    const hours = Math.floor(totalMinutes / 60);
    const minutes = totalMinutes % 60;

    if (hours === 0) {
        // A first appearance on the board is often a few seconds, and reading
        // it as "0m" looks like the plugin lost it.
        if (minutes === 0) {
            return seconds > 0 ? `${Math.floor(seconds)}s` : '0m';
        }

        return `${minutes}m`;
    }
    if (hours < 24 && minutes > 0) {
        return `${hours}h ${minutes}m`;
    }

    return `${hours}h`;
}

// Only ranks 1-3 ever reached this while the boards stopped at the podium, so
// the old lookup table was fine and "21th" was unreachable. The boards now list
// everyone.
export function ordinal(rank: number): string {
    const teens = rank % 100;
    if (teens >= 11 && teens <= 13) {
        return `${rank}th`;
    }

    switch (rank % 10) {
    case 1:
        return `${rank}st`;
    case 2:
        return `${rank}nd`;
    case 3:
        return `${rank}rd`;
    default:
        return `${rank}th`;
    }
}
