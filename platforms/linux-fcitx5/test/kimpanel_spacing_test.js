// GNOME 50 / Mutter 18 native St regression. Run with a private D-Bus and runtime directory.
// ARGV[0]: private, patched Kimpanel indicator.js; ARGV[1]: Yaru or Yaru-dark.
import Meta from 'gi://Meta?version=18';
import St from 'gi://St?version=18';
import Gio from 'gi://Gio';
import GLib from 'gi://GLib';
import GObject from 'gi://GObject';
import System from 'system';

const context = Meta.create_context('0c-spacing-test');
const Plugin = GObject.registerClass(class extends Meta.Plugin {});
context.set_plugin_gtype(Plugin.$gtype);
context.configure(['0c-spacing-test', '--wayland', '--no-x11', '--headless', '--virtual-monitor', '800x600']);
context.setup();
context.start();
function run() {
    const stage = context.get_backend().get_stage();
    const themeContext = St.ThemeContext.get_for_stage(stage);
    const themeName = ARGV[1] || 'Yaru-dark';
    themeContext.set_theme(new St.Theme({application_stylesheet: Gio.File.new_for_path('/usr/share/gnome-shell/theme/' + themeName + '/gnome-shell.css')}));
    const panel = new St.BoxLayout({name: 'panel'});
    const button = new St.BoxLayout({style_class: 'panel-button'});
    const box = new St.BoxLayout({style_class: 'panel-status-menu-box'});
    const icon = new St.Icon({style_class: 'system-status-icon', icon_size: 16});
    stage.add_child(panel); panel.add_child(button); button.add_child(box); box.add_child(icon);
    stage.show(); panel.show(); button.show(); box.show(); icon.show();
    const decoder = new TextDecoder();
    const source = decoder.decode(GLib.file_get_contents(ARGV[0])[1]);
    const method = source.slice(source.indexOf('    _setIcon(property) {'), source.indexOf('    _active() {'));
    const Indicator = new Function('Lib', 'return class {' + method + '}')({
        createIcon: name => Gio.ThemedIcon.new_with_default_fallbacks(name + '-symbolic-hack'),
        extractLabelString: label => label,
    });
    const controller = new Indicator();
    controller.mainIcon = icon; controller.labelIcon = {visible: false, text: ''};
    function property(name, hint = []) { return {icon: name, label: 'text', hint}; }
    function width() { return icon.get_preferred_width(-1)[1]; }
    function assert(ok, text) { if (!ok) throw Error(text); }
    const results = [];
    controller._setIcon(property('input-keyboard'));
    const initial = width();
    results.push({mode: 'foreign', width: initial, style: icon.get_style()});
    for (const name of ['0c-zhuyin', '0c-english', '0c-zhuyin']) {
        controller._setIcon(property(name));
        const compact = width();
        assert(Math.abs(initial - compact - 20) < .01, `spacing did not decrease by 20: ${initial}, ${compact}`);
        assert(icon.icon_size === 16 && icon.visible && !controller.labelIcon.visible, 'icon size or presentation changed');
        results.push({mode: name, width: compact, style: icon.get_style()});
    }
    controller._setIcon(property('other-input-method'));
    assert(width() === initial && icon.get_style() === null, 'foreign icon style not restored');
    results.push({mode: 'foreign-restored', width: width(), style: icon.get_style()});
    icon.set_style('opacity: 200;');
    controller._setIcon(property('0c-english'));
    controller._setIcon(property('input-keyboard'));
    assert(icon.get_style() === 'opacity: 200;', 'existing inline style lost');
    controller._setIcon(property('0c-zhuyin'));
    controller._setIcon(property('', ['label=zh']));
    assert(!icon.visible && controller.labelIcon.visible && controller.labelIcon.text === 'zh', 'text indicator changed');
    assert(icon.get_style() === 'opacity: 200;', 'text indicator did not restore original style');
    print(JSON.stringify({pass: true, theme: themeName, results, existing_style_preserved: true, text_hint_preserved: true}));
    // Dispose St objects while Mutter's backend is still alive.
    panel.destroy();
    themeContext.run_dispose();
    St.TextureCache.get_default().run_dispose();
}
run();
System.gc();
System.exit(0);
