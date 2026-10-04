import UserNotifications

// Notifications shows the server's notifications ({title, body, url, tag}, see
// backend/internal/service/notifications.go), which the web app hands over while it runs
// (frontend/src/lib/desktop.ts: Web Push doesn't reach a WKWebView). Clicking one opens its page
// in the app. Like the Android app's (mobile/android/…/Notifications.java).
enum Notifications {
    // show shows a notification from the server; a newer one with the same tag replaces it.
    static func show(_ message: [String: Any]) {
        let title = clip(message["title"] as? String ?? "", 200)
        let content = UNMutableNotificationContent()
        content.title = title.isEmpty ? "knowpod" : title
        content.body = clip(message["body"] as? String ?? "", 500)
        content.sound = .default
        if let url = message["url"] as? String, !url.isEmpty { content.userInfo = ["url": url] }
        let tag = message["tag"] as? String ?? ""
        let id = tag.isEmpty ? "untagged-" + UUID().uuidString : "tag-" + tag
        UNUserNotificationCenter.current().add(UNNotificationRequest(identifier: id, content: content, trigger: nil), withCompletionHandler: nil)
    }

    // askOnce asks for notifications the first time a page of the server is ready: the
    // server's reminders need them.
    static func askOnce() {
        let defaults = UserDefaults.standard
        if defaults.bool(forKey: "notificationsAsked") { return }
        defaults.set(true, forKey: "notificationsAsked")
        UNUserNotificationCenter.current().requestAuthorization(options: [.alert, .sound, .badge]) { _, _ in }
    }

    private static func clip(_ text: String, _ max: Int) -> String {
        text.count > max ? String(text.prefix(max)) : text
    }
}

// NotificationDelegate shows notifications also while the app is in front, and opens the page
// of a clicked one.
final class NotificationDelegate: NSObject, UNUserNotificationCenterDelegate {
    func userNotificationCenter(_ center: UNUserNotificationCenter, willPresent notification: UNNotification,
                                withCompletionHandler completionHandler: @escaping (UNNotificationPresentationOptions) -> Void) {
        completionHandler([.banner, .list, .sound])
    }

    func userNotificationCenter(_ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse,
                                withCompletionHandler completionHandler: @escaping () -> Void) {
        let path = response.notification.request.content.userInfo["url"] as? String
        completionHandler()
        guard let path else { return }
        Task { @MainActor in
            MainViewController.open(path)
        }
    }
}
