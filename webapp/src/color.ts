// contrastingSimpleColor mirrors Mattermost's own getContrastingSimpleColor
// (mattermost-redux/utils/theme_utils), so the plugin's banner picks the same
// text colour Mattermost's classification banner would for a given background.
export function contrastingSimpleColor(colorHexCode: string): string {
    const color = colorHexCode.startsWith('#') ? colorHexCode.slice(1) : colorHexCode;

    if (color.length !== 6) {
        return '#000000';
    }

    const red = parseInt(color.substring(0, 2), 16);
    const green = parseInt(color.substring(2, 4), 16);
    const blue = parseInt(color.substring(4, 6), 16);

    // Relative luminance, per https://www.w3.org/TR/WCAG21/#dfn-relative-luminance
    const [redLuminance, greenLuminance, blueLuminance] = [red / 255, green / 255, blue / 255].map((channel) => {
        if (channel <= 0.04045) {
            return channel / 12.92;
        }
        return Math.pow((channel + 0.055) / 1.055, 2.4);
    });

    const luminance = (0.2126 * redLuminance) + (0.7152 * greenLuminance) + (0.0722 * blueLuminance);

    return luminance > 0.179 ? '#000000' : '#FFFFFF';
}
