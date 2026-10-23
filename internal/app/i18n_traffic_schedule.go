package app

// Server messages of per-client traffic resets and automatic renewal.
//
// Order: en, zh-TW, ja, ru, fa, vi, es, tr, uk, pt-BR.
var trafficScheduleMessages = map[string][10]string{
	"流量重置周期无效":            {"Invalid traffic reset period", "流量重置週期無效", "通信量リセットの周期が無効です", "Неверный период сброса трафика", "دورهٔ بازنشانی ترافیک نامعتبر است", "Chu kỳ đặt lại lưu lượng không hợp lệ", "Periodo de reinicio de tráfico no válido", "Trafik sıfırlama dönemi geçersiz", "Неправильний період скидання трафіку", "Período de redefinição de tráfego inválido"},
	"每月重置日必须在 1-31 之间":    {"The monthly reset day must be between 1 and 31", "每月重置日必須在 1-31 之間", "毎月のリセット日は 1〜31 で指定してください", "День ежемесячного сброса должен быть от 1 до 31", "روز بازنشانی ماهانه باید بین ۱ تا ۳۱ باشد", "Ngày đặt lại hằng tháng phải từ 1 đến 31", "El día de reinicio mensual debe estar entre 1 y 31", "Aylık sıfırlama günü 1 ile 31 arasında olmalı", "День щомісячного скидання має бути від 1 до 31", "O dia da redefinição mensal deve estar entre 1 e 31"},
	"自动续期设置无效":            {"Invalid auto-renewal settings", "自動續期設定無效", "自動更新の設定が無効です", "Неверные настройки автопродления", "تنظیمات تمدید خودکار نامعتبر است", "Cài đặt tự động gia hạn không hợp lệ", "Ajustes de renovación automática no válidos", "Otomatik yenileme ayarları geçersiz", "Неправильні налаштування автоподовження", "Configurações de renovação automática inválidas"},
	"自动续期只能选择一种方式":        {"Choose only one auto-renewal method", "自動續期只能選擇一種方式", "自動更新の方式は 1 つだけ選んでください", "Выберите только один способ автопродления", "فقط یک روش تمدید خودکار انتخاب کنید", "Chỉ chọn một cách tự động gia hạn", "Elige solo un método de renovación automática", "Yalnızca bir otomatik yenileme yöntemi seçin", "Виберіть лише один спосіб автоподовження", "Escolha apenas um método de renovação automática"},
	"自动续期天数必须在 1-3650 之间": {"Renewal days must be between 1 and 3650", "自動續期天數必須在 1-3650 之間", "更新日数は 1〜3650 で指定してください", "Срок продления должен быть от 1 до 3650 дней", "تعداد روزهای تمدید باید بین ۱ تا ۳۶۵۰ باشد", "Số ngày gia hạn phải từ 1 đến 3650", "Los días de renovación deben estar entre 1 y 3650", "Yenileme günü 1 ile 3650 arasında olmalı", "Строк подовження має бути від 1 до 3650 днів", "Os dias de renovação devem estar entre 1 e 3650"},
	"每月续期日必须在 1-31 之间":    {"The monthly renewal day must be between 1 and 31", "每月續期日必須在 1-31 之間", "毎月の更新日は 1〜31 で指定してください", "День ежемесячного продления должен быть от 1 до 31", "روز تمدید ماهانه باید بین ۱ تا ۳۱ باشد", "Ngày gia hạn hằng tháng phải từ 1 đến 31", "El día de renovación mensual debe estar entre 1 y 31", "Aylık yenileme günü 1 ile 31 arasında olmalı", "День щомісячного подовження має бути від 1 до 31", "O dia da renovação mensal deve estar entre 1 e 31"},
	"每周续期日必须在 1-7 之间":     {"The weekly renewal day must be between 1 and 7", "每週續期日必須在 1-7 之間", "毎週の更新曜日は 1〜7 で指定してください", "День еженедельного продления должен быть от 1 до 7", "روز تمدید هفتگی باید بین ۱ تا ۷ باشد", "Ngày gia hạn hằng tuần phải từ 1 đến 7", "El día de renovación semanal debe estar entre 1 y 7", "Haftalık yenileme günü 1 ile 7 arasında olmalı", "День щотижневого подовження має бути від 1 до 7", "O dia da renovação semanal deve estar entre 1 e 7"},
}

func init() {
	languages := []string{"zh-TW", "ja", "ru", "fa", "vi", "es", "tr", "uk", "pt-BR"}
	for source, translations := range trafficScheduleMessages {
		messagesEN[source] = translations[0]
		for i, language := range languages {
			messagesLocalized[language][source] = translations[i+1]
		}
	}
}
