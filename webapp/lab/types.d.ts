// The lab inlines the real fonts so the panel is drawn in the typeface it will
// actually ship with. webpack turns these imports into data URIs; nothing is
// fetched, here or in production.
declare module '*.woff2' {
    const url: string;
    export default url;
}

declare module '*.svg' {
    const url: string;
    export default url;
}

declare module '*.png' {
    const url: string;
    export default url;
}

declare module '*.jpg' {
    const url: string;
    export default url;
}

// The sample clip the lab plays in place of a real one: a two-minute test
// pattern with a beep, made by build/make-lab-clip.sh. Only the lab's mock
// client imports it; the plugin itself never does.
declare module '*.mp4' {
    const url: string;
    export default url;
}
