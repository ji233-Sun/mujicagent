#include "webkit_abi.h"
#include "capture_policy.h"
#include <ImageIO/ImageIO.h>
#include <cmath>
#include <cstring>
#include <string>
#include <vector>

namespace {
constexpr size_t maxPNG = 4 * 1024 * 1024;
WKRef world;
struct Transaction {
    WKRef page;
    JSContextRef context;
    std::vector<unsigned char> wire;
    bool called = false;
};
Transaction* active;

void u32(std::vector<unsigned char>& b, size_t offset, uint32_t value) {
    for (int i = 0; i < 4; ++i) b[offset+i] = value >> (24 - i*8);
}

JSValueRef take(JSContextRef context, JSObjectRef, JSObjectRef, size_t count,
                const JSValueRef arguments[], JSValueRef*) {
    auto* t = active;
    if (!t || t->context != context || t->called || count != 2)
        return JSValueMakeBoolean(context, false);
    t->called = true;
    double w = JSValueToNumber(context, arguments[0], nullptr);
    double h = JSValueToNumber(context, arguments[1], nullptr);
    if (!std::isfinite(w) || !std::isfinite(h) || w < 1 || h < 1 ||
        w > 4096 || h > 4096 || w != std::floor(w) || h != std::floor(h))
        return JSValueMakeBoolean(context, false);
    double scale = WKBundlePageGetBackingScaleFactor(t->page);
    if (!std::isfinite(scale) || scale < 1 || scale > 4 ||
        w * scale > 8192 || h * scale > 8192 || w * h * scale * scale > 16777216)
        return JSValueMakeBoolean(context, false);
    WKRef image = WKBundlePageCreateSnapshotInViewCoordinates(t->page, {{0, 0}, {w, h}}, 0);
    if (!image) return JSValueMakeBoolean(context, false);
    CGImageRef cg = WKImageCreateCGImage(image);
    WKRelease(image);
    if (!cg) return JSValueMakeBoolean(context, false);
    size_t width = CGImageGetWidth(cg), height = CGImageGetHeight(cg);
    if (!width || !height || width > 8192 || height > 8192 || width * height > 16777216) {
        CGImageRelease(cg);
        return JSValueMakeBoolean(context, false);
    }
    CFMutableDataRef data = CFDataCreateMutable(nullptr, 0);
    auto destination = CGImageDestinationCreateWithData(data, CFSTR("public.png"), 1, nullptr);
    bool ok = false;
    if (destination) {
        CGImageDestinationAddImage(destination, cg, nullptr);
        ok = CGImageDestinationFinalize(destination) && CFDataGetLength(data) > 0 &&
             CFDataGetLength(data) <= static_cast<CFIndex>(maxPNG);
        CFRelease(destination);
    }
    CGImageRelease(cg);
    if (ok) {
        u32(t->wire, 24, width); u32(t->wire, 28, height);
        t->wire.insert(t->wire.end(), CFDataGetBytePtr(data), CFDataGetBytePtr(data) + CFDataGetLength(data));
    }
    CFRelease(data);
    return JSValueMakeBoolean(context, ok);
}

void message(WKRef, WKRef page, WKRef name, WKRef body, const void*) {
    if (active || !WKStringIsEqualToUTF8CString(name, "mujicagent.capture.v1") ||
        !body || WKGetTypeID(body) != WKDataGetTypeID()) return;
    size_t size = WKDataGetSize(body);
    if (size < 57 || size > 65592) return;
    const auto* bytes = WKDataGetBytes(body);
    if (std::memcmp(bytes, "MCAP\1", 5) || bytes[5] > 1 || bytes[6] || bytes[7] ||
        std::memcmp(bytes+24, captureDigest, 32)) return;
    unsigned mode = bytes[5];
    Transaction t {page, nullptr, std::vector<unsigned char>(32, 0)};
    std::memcpy(t.wire.data(), bytes, 24);
    t.wire[5] = 1; // fail closed; no bitmap until policy and painter succeed
    t.wire[6] = mode;
    auto frame = WKBundlePageGetMainFrame(page);
    if (frame) t.context = WKBundleFrameGetJavaScriptContextForWorld(frame, world);
    if (t.context) {
        // Window expandos in this world are private. A Symbol key also avoids
        // page-controlled DOM named properties during first installation.
        auto install = JSStringCreateWithUTF8CString((std::string(
            "(()=>{const key=Symbol.for('mujicagent.browser.refs.v1');if(!window[key])window[key]=")
            + pageSource + ";return window[key];})()").c_str());
        JSValueRef exception = nullptr;
        auto references = JSEvaluateScript(t.context, install, nullptr, nullptr, 1, &exception);
        JSStringRelease(install);
        const char* eval = "const q=JSON.parse(expectedOrigin);if(location.origin!==q.origin)return JSON.stringify({code:'stale'});return window.__mujicBrowser(q.action,q.args);";
        auto source = JSStringCreateWithUTF8CString(mode ? eval : capturePolicy);
        auto takeName = JSStringCreateWithUTF8CString("take");
        auto originName = JSStringCreateWithUTF8CString("expectedOrigin");
        auto refsName = JSStringCreateWithUTF8CString("references");
        JSStringRef parameters[] = {takeName, originName, refsName};
        auto fn = exception ? nullptr : JSObjectMakeFunction(t.context, nullptr, 3, parameters, source, nullptr, 1, &exception);
        if (fn) JSValueProtect(t.context, fn);
        auto origin = JSStringCreateWithUTF8CString(std::string(reinterpret_cast<const char*>(bytes+56), size-56).c_str());
        JSValueRef arguments[3];
        arguments[0] = JSObjectMakeFunctionWithCallback(t.context, nullptr, take);
        JSValueProtect(t.context, arguments[0]);
        arguments[1] = JSValueMakeString(t.context, origin);
        JSValueProtect(t.context, arguments[1]);
        arguments[2] = references ? references : JSValueMakeNull(t.context);
        active = &t;
        auto result = fn && !exception ? JSObjectCallAsFunction(t.context, fn, nullptr, 3, arguments, &exception) : nullptr;
        active = nullptr;
        if (mode && result && !exception && JSValueIsString(t.context, result)) {
            auto value = JSValueToStringCopy(t.context, result, &exception);
            size_t maximum = value ? JSStringGetMaximumUTF8CStringSize(value) : 0;
            if (!exception && maximum && maximum <= 196609) {
                t.wire.resize(32 + maximum);
                size_t count = JSStringGetUTF8CString(value, reinterpret_cast<char*>(t.wire.data()+32), maximum);
                if (count && count <= 65537) {
                    t.wire.resize(32 + count - 1);
                    t.wire[5] = 0;
                }
            }
            if (value) JSStringRelease(value);
        } else if (!mode && result && !exception && JSValueIsBoolean(t.context, result) &&
                   JSValueToBoolean(t.context, result) && t.wire.size() > 32)
            t.wire[5] = 0;
        if (t.wire[5]) t.wire.resize(32);
        JSValueUnprotect(t.context, arguments[0]); JSValueUnprotect(t.context, arguments[1]);
        if (fn) JSValueUnprotect(t.context, fn);
        JSStringRelease(origin); JSStringRelease(source);
        JSStringRelease(takeName); JSStringRelease(originName); JSStringRelease(refsName);
    }
    auto responseName = WKStringCreateWithUTF8CString("mujicagent.capture.v1.result");
    auto response = WKDataCreate(t.wire.data(), t.wire.size());
    WKBundlePagePostMessage(page, responseName, response);
    WKRelease(responseName); WKRelease(response);
}
}

extern "C" __attribute__((visibility("default"))) void WKBundleInitialize(WKRef bundle, WKRef) {
    world = WKBundleScriptWorldCreateWorld();
    WKBundleScriptWorldMakeAllShadowRootsOpen(world);
    WKBundleScriptWorldDisableOverrideBuiltinsBehavior(world);
    WKBundleClientV1 client {};
    client.base.version = 1;
    client.pageMessage = message;
    WKBundleSetClient(bundle, &client.base);
}
