package net.kleinhenz.knowpod;

import com.getcapacitor.JSObject;
import com.getcapacitor.Plugin;
import com.getcapacitor.PluginCall;
import com.getcapacitor.PluginMethod;
import com.getcapacitor.annotation.CapacitorPlugin;

// SetupPlugin is the call of the bundled setup page (www/), which asks for the knowpod
// server's address. Capacitor offers it only to the app's own pages (https://localhost), never
// to a page the server sent.
@CapacitorPlugin(name = "KnowpodSetup")
public class SetupPlugin extends Plugin {
    // get returns the server set now and why loading it failed, if it did.
    @PluginMethod
    public void get(PluginCall call) {
        MainActivity activity = (MainActivity) getActivity();
        JSObject result = new JSObject();
        String current = ServerConfig.serverUrl(getContext());
        result.put("current", current == null ? "" : current);
        result.put("error", activity.setupError());
        call.resolve(result);
    }

    // setServer saves the server and loads it.
    @PluginMethod
    public void setServer(PluginCall call) {
        String url = ServerConfig.normalize(call.getString("url"));
        JSObject result = new JSObject();
        if (url == null) {
            result.put("ok", false);
            call.resolve(result);
            return;
        }
        ServerConfig.setServerUrl(getContext(), url);
        result.put("ok", true);
        result.put("url", url);
        call.resolve(result);
        MainActivity activity = (MainActivity) getActivity();
        activity.runOnUiThread(activity::loadServer);
    }

    // cancel goes back to the server set now.
    @PluginMethod
    public void cancel(PluginCall call) {
        call.resolve();
        MainActivity activity = (MainActivity) getActivity();
        activity.runOnUiThread(activity::loadServer);
    }
}
