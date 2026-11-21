//go:build darwin && !ios

// macOS 菜单栏图标与 Dock 行为, 由 tray_darwin.go 调用。
// 所有 AppKit 操作都派发到主线程; Wails 的对象是手动引用计数(无 ARC)。

#import <Cocoa/Cocoa.h>
#import <objc/runtime.h>
#include <stdlib.h>

#include "_cgo_export.h"

@interface CZLTrayTarget : NSObject
- (void)openWindow:(id)sender;
- (void)quitApp:(id)sender;
@end

@implementation CZLTrayTarget
- (void)openWindow:(id)sender {
    czlTrayOpen();
}
- (void)quitApp:(id)sender {
    czlTrayQuit();
}
@end

static NSStatusItem *czlStatusItem = nil;
static CZLTrayTarget *czlTarget = nil;
// 菜单项留着引用, 界面换语言时改标题 (czlTraySetMenuText)。
static NSMenuItem *czlOpenItem = nil;
static NSMenuItem *czlQuitItem = nil;

// 点 Dock 图标时, 窗口已被隐藏(关闭即隐藏)就把它找回来。
static BOOL czlShouldHandleReopen(id self, SEL _cmd, NSApplication *app, BOOL hasVisibleWindows) {
    if (!hasVisibleWindows) {
        czlTrayOpen();
    }
    return YES;
}

static void czlInstallReopenHandler(void) {
    id delegate = [NSApp delegate];
    if (delegate == nil) {
        return;
    }
    // Wails 的 AppDelegate 没实现这个方法才补上; 以后它自带实现时 class_addMethod 返回 NO, 保持原样。
    class_addMethod([delegate class], @selector(applicationShouldHandleReopen:hasVisibleWindows:),
                    (IMP)czlShouldHandleReopen, "c@:@c");
}

// open / quit 是菜单文字; 在调用线程上拷贝成 NSString, 调用方返回后即可释放。
// png1x / png2x 是 18pt 菜单栏 template 图标的 1x 与 2x 位图, 由 C.CBytes 分配, 这里拷贝后释放。
void czlTrayStart(const void *png1x, int len1x, const void *png2x, int len2x, const char *open, const char *quit) {
    NSData *data1x = [[NSData alloc] initWithBytes:png1x length:(NSUInteger)len1x];
    NSData *data2x = [[NSData alloc] initWithBytes:png2x length:(NSUInteger)len2x];
    free((void *)png1x);
    free((void *)png2x);
    NSString *openTitle = [[NSString alloc] initWithUTF8String:open];
    NSString *quitTitle = [[NSString alloc] initWithUTF8String:quit];

    dispatch_async(dispatch_get_main_queue(), ^{
        czlInstallReopenHandler();
        if (czlStatusItem != nil) {
            [data1x release];
            [data2x release];
            [openTitle release];
            [quitTitle release];
            return;
        }

        czlTarget = [[CZLTrayTarget alloc] init];
        czlStatusItem = [[[NSStatusBar systemStatusBar] statusItemWithLength:NSVariableStatusItemLength] retain];

        // 两份位图挂在同一个 18pt 的 NSImage 上, AppKit 按屏幕倍率挑; template 让系统按亮暗菜单栏着色。
        NSImage *image = [[NSImage alloc] initWithSize:NSMakeSize(18, 18)];
        for (NSData *d in @[ data1x, data2x ]) {
            NSBitmapImageRep *rep = [[NSBitmapImageRep alloc] initWithData:d];
            if (rep != nil) {
                [rep setSize:NSMakeSize(18, 18)];
                [image addRepresentation:rep];
                [rep release];
            }
        }
        [data1x release];
        [data2x release];
        [image setTemplate:YES];
        NSStatusBarButton *button = [czlStatusItem button];
        [button setImage:image];
        [button setImagePosition:NSImageLeft];
        [button setToolTip:@"CZL Mail"];
        [image release];

        NSMenu *menu = [[NSMenu alloc] initWithTitle:@"CZL Mail"];
        czlOpenItem = [[NSMenuItem alloc] initWithTitle:openTitle action:@selector(openWindow:) keyEquivalent:@""];
        [czlOpenItem setTarget:czlTarget];
        [menu addItem:czlOpenItem];
        [menu addItem:[NSMenuItem separatorItem]];
        czlQuitItem = [[NSMenuItem alloc] initWithTitle:quitTitle action:@selector(quitApp:) keyEquivalent:@""];
        [czlQuitItem setTarget:czlTarget];
        [menu addItem:czlQuitItem];
        [openTitle release];
        [quitTitle release];
        [czlStatusItem setMenu:menu];
        [menu release];
    });
}

void czlTraySetMenuText(const char *open, const char *quit) {
    NSString *openTitle = [[NSString alloc] initWithUTF8String:open];
    NSString *quitTitle = [[NSString alloc] initWithUTF8String:quit];
    dispatch_async(dispatch_get_main_queue(), ^{
        if (czlOpenItem != nil) {
            [czlOpenItem setTitle:openTitle];
        }
        if (czlQuitItem != nil) {
            [czlQuitItem setTitle:quitTitle];
        }
        [openTitle release];
        [quitTitle release];
    });
}

void czlTrayStop(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
        if (czlStatusItem == nil) {
            return;
        }
        [[NSStatusBar systemStatusBar] removeStatusItem:czlStatusItem];
        [czlStatusItem release];
        czlStatusItem = nil;
        [czlOpenItem release];
        czlOpenItem = nil;
        [czlQuitItem release];
        czlQuitItem = nil;
    });
}

// 未读数同时显示在菜单栏图标右侧与 Dock 图标角标上; 0 时都清掉。
// tooltip 是悬停提示; 在调用线程上拷贝, 调用方返回后即可释放。
void czlTraySetUnread(int n, const char *tooltip) {
    NSString *tip = [[NSString alloc] initWithUTF8String:tooltip];
    dispatch_async(dispatch_get_main_queue(), ^{
        NSString *label = n > 0 ? [NSString stringWithFormat:@"%d", n] : nil;
        [[NSApp dockTile] setBadgeLabel:label];
        if (czlStatusItem == nil) {
            [tip release];
            return;
        }
        NSStatusBarButton *button = [czlStatusItem button];
        [button setTitle:(label != nil ? [@" " stringByAppendingString:label] : @"")];
        [button setToolTip:tip];
        [tip release];
    });
}

void czlActivateApp(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
        [NSApp activateIgnoringOtherApps:YES];
    });
}
