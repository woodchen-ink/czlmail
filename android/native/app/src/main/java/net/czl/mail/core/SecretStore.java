package net.czl.mail.core;

import android.content.Context;
import android.content.SharedPreferences;
import android.security.keystore.KeyGenParameterSpec;
import android.security.keystore.KeyProperties;
import android.util.Base64;
import java.nio.ByteBuffer;
import java.nio.charset.StandardCharsets;
import java.security.GeneralSecurityException;
import java.security.KeyStore;
import javax.crypto.Cipher;
import javax.crypto.KeyGenerator;
import javax.crypto.SecretKey;
import javax.crypto.spec.GCMParameterSpec;

/**
 * 凭据存储 (App Password、OAuth 令牌、AI 密钥), 对应桌面端的系统密钥库。
 *
 * 密文存在本程序私有的 SharedPreferences, 密钥在 Android Keystore 里生成且不可导出 (有硬件时由 TEE 保管)。
 * 程序关了备份 (allowBackup=false): 密钥不随备份迁移, 恢复到别的设备的密文也解不开。
 * 没有明文回落: Keystore 不可用时直接报错, 与桌面端"密钥库不可用时登录失败"一致。
 */
final class SecretStore {
    private static final String KEY_ALIAS = "czlmail-secrets";
    private static final String PREFS = "czlmail-secrets";
    private static final int IV_LEN = 12;

    private SecretStore() {}

    private static SharedPreferences prefs(Context ctx) {
        return ctx.getSharedPreferences(PREFS, Context.MODE_PRIVATE);
    }

    private static String entry(String service, String user) {
        return service + "\n" + user;
    }

    static synchronized String get(Context ctx, String service, String user) throws GeneralSecurityException {
        String stored = prefs(ctx).getString(entry(service, user), null);
        if (stored == null) return null;
        byte[] raw = Base64.decode(stored, Base64.NO_WRAP);
        ByteBuffer buf = ByteBuffer.wrap(raw);
        byte[] iv = new byte[IV_LEN];
        buf.get(iv);
        byte[] sealed = new byte[buf.remaining()];
        buf.get(sealed);
        Cipher c = Cipher.getInstance("AES/GCM/NoPadding");
        c.init(Cipher.DECRYPT_MODE, key(), new GCMParameterSpec(128, iv));
        return new String(c.doFinal(sealed), StandardCharsets.UTF_8);
    }

    static synchronized void set(Context ctx, String service, String user, String value) throws GeneralSecurityException {
        Cipher c = Cipher.getInstance("AES/GCM/NoPadding");
        c.init(Cipher.ENCRYPT_MODE, key());
        byte[] iv = c.getIV();
        byte[] sealed = c.doFinal(value.getBytes(StandardCharsets.UTF_8));
        byte[] raw = ByteBuffer.allocate(iv.length + sealed.length).put(iv).put(sealed).array();
        if (!prefs(ctx).edit().putString(entry(service, user), Base64.encodeToString(raw, Base64.NO_WRAP)).commit()) {
            throw new GeneralSecurityException("write secret failed");
        }
    }

    /** 返回条目是否存在。 */
    static synchronized boolean delete(Context ctx, String service, String user) {
        SharedPreferences p = prefs(ctx);
        String k = entry(service, user);
        if (!p.contains(k)) return false;
        p.edit().remove(k).commit();
        return true;
    }

    private static SecretKey key() throws GeneralSecurityException {
        try {
            KeyStore ks = KeyStore.getInstance("AndroidKeyStore");
            ks.load(null);
            KeyStore.Entry e = ks.getEntry(KEY_ALIAS, null);
            if (e instanceof KeyStore.SecretKeyEntry) return ((KeyStore.SecretKeyEntry) e).getSecretKey();
            KeyGenerator g = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore");
            g.init(new KeyGenParameterSpec.Builder(KEY_ALIAS, KeyProperties.PURPOSE_ENCRYPT | KeyProperties.PURPOSE_DECRYPT)
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                .setKeySize(256)
                .build());
            return g.generateKey();
        } catch (java.io.IOException e) {
            throw new GeneralSecurityException("open keystore", e);
        }
    }
}
