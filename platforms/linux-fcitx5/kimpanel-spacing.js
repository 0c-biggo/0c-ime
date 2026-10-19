// Only the 0c brand icons need compact spacing; retain the theme's icon size.
{
    const compact = property.icon === '0c-zhuyin' || property.icon === '0c-english';
    if (compact && !this._0cCompactIcon) {
        this._0cOriginalIconStyle = this.mainIcon.get_style();
        const original = this._0cOriginalIconStyle ? this._0cOriginalIconStyle + '; ' : '';
        this.mainIcon.set_style(original + 'padding: 0; margin: 0;');
    } else if (!compact && this._0cCompactIcon) {
        this.mainIcon.set_style(this._0cOriginalIconStyle);
    }
    this._0cCompactIcon = compact;
}
