#include "clock.h"
#include "board.h"
#include <Wire.h>
#include <sys/time.h>
#include <esp_sntp.h>
#include <SensorPCF85063.hpp>

static SensorPCF85063 rtc;
static bool rtc_ok = false;
static bool valid = false;
static String tz;
static volatile bool ntp_synced = false;

// Days since 1970-01-01 for a proleptic Gregorian date (H. Hinnant's algorithm)
static int64_t days_from_civil(int y, unsigned m, unsigned d)
{
    y -= m <= 2;
    int64_t era = (y >= 0 ? y : y - 399) / 400;
    unsigned yoe = (unsigned)(y - era * 400);
    unsigned doy = (153 * (m + (m > 2 ? -3 : 9)) + 2) / 5 + d - 1;
    unsigned doe = yoe * 365 + yoe / 4 - yoe / 100 + doy;
    return era * 146097 + (int64_t)doe - 719468;
}

bool clock_begin(const char *posix_tz)
{
    tz = posix_tz;
    setenv("TZ", tz.c_str(), 1);
    tzset();

    rtc_ok = rtc.begin(Wire, PIN_I2C_SDA, PIN_I2C_SCL);
    if (!rtc_ok) {
        Serial.println("RTC init FAILED");
        return false;
    }

    RTC_DateTime dt = rtc.getDateTime();
    if (!rtc.isClockIntegrityGuaranteed() || dt.getYear() < 2025) {
        Serial.println("RTC not set; waiting for NTP");
        return true;
    }

    time_t t = days_from_civil(dt.getYear(), dt.getMonth(), dt.getDay()) * 86400
             + dt.getHour() * 3600 + dt.getMinute() * 60 + dt.getSecond();
    timeval tv = {t, 0};
    settimeofday(&tv, nullptr);
    valid = true;
    Serial.printf("Time from RTC: %s\n", clock_format("%Y-%m-%d %H:%M:%S %Z").c_str());
    return true;
}

static void on_ntp_sync(timeval *)
{
    ntp_synced = true;  // RTC is written from clock_poll() on the main task
}

void clock_start_ntp()
{
    static bool started = false;
    if (started) return;
    started = true;
    sntp_set_time_sync_notification_cb(on_ntp_sync);
    configTzTime(tz.c_str(), "pool.ntp.org", "time.google.com");
}

void clock_poll()
{
    if (!ntp_synced) return;
    ntp_synced = false;
    valid = true;

    if (rtc_ok) {
        time_t now = time(nullptr);
        tm utc;
        gmtime_r(&now, &utc);
        rtc.setDateTime(RTC_DateTime(utc));
    }
    Serial.printf("Time from NTP: %s\n", clock_format("%Y-%m-%d %H:%M:%S %Z").c_str());
}

bool clock_valid()
{
    return valid;
}

String clock_format(const char *fmt)
{
    if (!valid) return "--:--";
    time_t now = time(nullptr);
    tm local;
    localtime_r(&now, &local);
    char buf[64];
    strftime(buf, sizeof(buf), fmt, &local);
    return buf;
}
