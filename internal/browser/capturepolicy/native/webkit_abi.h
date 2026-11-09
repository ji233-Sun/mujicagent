// The subset of WebKit's versioned C ABI needed by the capture bundle.
// Signatures/layouts: WebKit 8e16cbe44f3a7e6aec2a641d612cba2b641cbf87,
// Source/WebKit/WebProcess/InjectedBundle/API/c and Shared/API/c.
// System WebKit provides these symbols; no engine implementation is bundled.
#pragma once
#include <JavaScriptCore/JavaScriptCore.h>
#include <CoreGraphics/CoreGraphics.h>
#include <cstddef>
#include <cstdint>
using WKRef = const void*;
struct WKClientBase { int version; const void* info; };
struct WKBundleClientV1 {
    WKClientBase base;
    void (*created)(WKRef, WKRef, const void*);
    void (*destroyed)(WKRef, WKRef, const void*);
    void (*group)(WKRef, WKRef, const void*);
    void (*message)(WKRef, WKRef, WKRef, const void*);
    void (*pageMessage)(WKRef, WKRef, WKRef, WKRef, const void*);
};
struct WKPoint { double x, y; };
struct WKSize { double width, height; };
struct WKRect { WKPoint origin; WKSize size; };
extern "C" {
void WKBundleSetClient(WKRef, const WKClientBase*);
WKRef WKBundlePageGetMainFrame(WKRef);
JSGlobalContextRef WKBundleFrameGetJavaScriptContextForWorld(WKRef, WKRef);
WKRef WKBundleScriptWorldCreateWorld();
void WKBundleScriptWorldMakeAllShadowRootsOpen(WKRef);
void WKBundleScriptWorldDisableOverrideBuiltinsBehavior(WKRef);
void WKBundlePagePostMessage(WKRef, WKRef, WKRef);
bool WKStringIsEqualToUTF8CString(WKRef, const char*);
WKRef WKStringCreateWithUTF8CString(const char*);
WKRef WKDataCreate(const unsigned char*, size_t);
const unsigned char* WKDataGetBytes(WKRef);
size_t WKDataGetSize(WKRef);
uint64_t WKDataGetTypeID();
uint64_t WKGetTypeID(WKRef);
WKRef WKBundlePageCreateSnapshotInViewCoordinates(WKRef, WKRect, unsigned);
double WKBundlePageGetBackingScaleFactor(WKRef);
CGImageRef WKImageCreateCGImage(WKRef);
void WKRelease(WKRef);
}
static_assert(offsetof(WKClientBase, info) == 8);
