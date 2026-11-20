"use client";

import { OptionSelect } from "@/components/option-select";
import { useI18n } from "@/components/locale-provider";
import { isLocale, LOCALES } from "@/lib/i18n";

/** Both entry points share the same device-local language preference. */
export function LanguageSelect() {
  const { locale, setLocale, t } = useI18n();
  return (
    <OptionSelect
      value={locale}
      options={LOCALES}
      onChange={(value) => {
        if (isLocale(value)) setLocale(value);
      }}
      aria-label={t("语言")}
    />
  );
}
