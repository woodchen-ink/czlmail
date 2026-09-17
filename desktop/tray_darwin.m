//go:build darwin

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

void czlTrayStart(const void *png, int length) {
    NSData *data = [[NSData alloc] initWithBytes:png length:(NSUInteger)length];
    free((void *)png);

    dispatch_async(dispatch_get_main_queue(), ^{
        czlInstallReopenHandler();
        if (czlStatusItem != nil) {
            [data release];
            return;
        }

        czlTarget = [[CZLTrayTarget alloc] init];
        czlStatusItem = [[[NSStatusBar systemStatusBar] statusItemWithLength:NSVariableStatusItemLength] retain];

        NSImage *image = [[NSImage alloc] initWithData:data];
        [data release];
        [image setSize:NSMakeSize(18, 18)];
        NSStatusBarButton *button = [czlStatusItem button];
        [button setImage:image];
        [button setImagePosition:NSImageLeft];
        [button setToolTip:@"CZL Mail"];
        [image release];

        NSMenu *menu = [[NSMenu alloc] initWithTitle:@"CZL Mail"];
        NSMenuItem *open = [[NSMenuItem alloc] initWithTitle:@"打开 CZL Mail" action:@selector(openWindow:) keyEquivalent:@""];
        [open setTarget:czlTarget];
        [menu addItem:open];
        [open release];
        [menu addItem:[NSMenuItem separatorItem]];
        NSMenuItem *quit = [[NSMenuItem alloc] initWithTitle:@"退出 CZL Mail" action:@selector(quitApp:) keyEquivalent:@""];
        [quit setTarget:czlTarget];
        [menu addItem:quit];
        [quit release];
        [czlStatusItem setMenu:menu];
        [menu release];
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
    });
}

// 未读数同时显示在菜单栏图标右侧与 Dock 图标角标上; 0 时都清掉。
void czlTraySetUnread(int n) {
    dispatch_async(dispatch_get_main_queue(), ^{
        NSString *label = n > 0 ? [NSString stringWithFormat:@"%d", n] : nil;
        [[NSApp dockTile] setBadgeLabel:label];
        if (czlStatusItem == nil) {
            return;
        }
        NSStatusBarButton *button = [czlStatusItem button];
        [button setTitle:(label != nil ? [@" " stringByAppendingString:label] : @"")];
        [button setToolTip:(label != nil ? [NSString stringWithFormat:@"CZL Mail — %d 封未读", n] : @"CZL Mail")];
    });
}

void czlActivateApp(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
        [NSApp activateIgnoringOtherApps:YES];
    });
}
