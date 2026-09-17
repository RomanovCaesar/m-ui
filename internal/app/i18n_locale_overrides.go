package app

// Human-reviewed translations override machine-generated text for the most
// visible API responses. The generated catalog still supplies every remaining
// message and every parameterized error.
func init() {
	overrides := map[string]map[string]string{
		"tr": {
			"请求无效": "Geçersiz istek", "账号或密码错误": "Kullanıcı adı veya parola yanlış", "设置已保存": "Ayarlar kaydedildi", "配置文件已生成": "Yapılandırma dosyası oluşturuldu", "入口已保存": "Giriş kaydedildi", "入口已删除": "Giriş silindi", "连接已关闭": "Bağlantı kapatıldı", "核心状态已更新": "Çekirdek durumu güncellendi", "流量统计已重置": "Trafik istatistikleri sıfırlandı", "界面语言无效": "Geçersiz arayüz dili", "入口不存在": "Giriş bulunamadı", "缺少入口 ID": "Giriş kimliği eksik", "入口名称不能为空": "Giriş adı boş olamaz", "入口类型不能为空": "Giriş türü boş olamaz", "密码不能为空": "Parola boş olamaz", "客户端已保存": "İstemci kaydedildi", "客户端已删除": "İstemci silindi", "Mihomo 配置测试通过": "Mihomo yapılandırma testi başarılı", "面板正在重启": "Panel yeniden başlatılıyor", "订阅路径无效": "Geçersiz abonelik yolu", "Clash 路径无效": "Geçersiz Clash yolu", "运行模式无效": "Geçersiz çalışma modu", "时区无效": "Geçersiz saat dilimi", "Snell 需要 PSK": "Snell için PSK gerekli", "Snell Reuse 仅支持 v4/v5": "Snell Reuse yalnızca v4/v5 sürümlerinde desteklenir", "Snell Obfs Host 必须是域名或 IP，不能包含协议、路径或空格": "Snell Obfs Host şema, yol veya boşluk içermeyen bir alan adı ya da IP olmalıdır",
		},
		"ru": {
			"请求无效": "Недействительный запрос", "账号或密码错误": "Неверное имя пользователя или пароль", "设置已保存": "Настройки сохранены", "配置文件已生成": "Конфигурация создана", "入口已保存": "Вход сохранён", "入口已删除": "Вход удалён", "连接已关闭": "Соединение закрыто", "核心状态已更新": "Состояние ядра обновлено", "流量统计已重置": "Статистика трафика сброшена", "界面语言无效": "Недопустимый язык интерфейса", "入口不存在": "Вход не найден", "缺少入口 ID": "Не указан ID входа", "入口名称不能为空": "Имя входа не может быть пустым", "入口类型不能为空": "Тип входа не может быть пустым", "密码不能为空": "Пароль не может быть пустым", "客户端已保存": "Клиент сохранён", "客户端已删除": "Клиент удалён", "Mihomo 配置测试通过": "Проверка конфигурации Mihomo пройдена", "面板正在重启": "Панель перезапускается", "订阅路径无效": "Недопустимый путь подписки", "Clash 路径无效": "Недопустимый путь Clash", "运行模式无效": "Недопустимый режим работы", "时区无效": "Недопустимый часовой пояс",
		},
		"fa": {
			"请求无效": "درخواست نامعتبر است", "账号或密码错误": "نام کاربری یا گذرواژه نادرست است", "设置已保存": "تنظیمات ذخیره شد", "配置文件已生成": "فایل پیکربندی ایجاد شد", "入口已保存": "ورودی ذخیره شد", "入口已删除": "ورودی حذف شد", "连接已关闭": "اتصال بسته شد", "核心状态已更新": "وضعیت هسته به‌روزرسانی شد", "流量统计已重置": "آمار ترافیک بازنشانی شد", "界面语言无效": "زبان رابط نامعتبر است", "入口不存在": "ورودی پیدا نشد", "缺少入口 ID": "شناسه ورودی وجود ندارد", "入口名称不能为空": "نام ورودی نمی‌تواند خالی باشد", "入口类型不能为空": "نوع ورودی نمی‌تواند خالی باشد", "密码不能为空": "گذرواژه نمی‌تواند خالی باشد", "客户端已保存": "کلاینت ذخیره شد", "客户端已删除": "کلاینت حذف شد", "Mihomo 配置测试通过": "آزمون پیکربندی Mihomo موفق بود", "面板正在重启": "پنل در حال راه‌اندازی مجدد است", "订阅路径无效": "مسیر اشتراک نامعتبر است", "Clash 路径无效": "مسیر Clash نامعتبر است", "运行模式无效": "حالت اجرا نامعتبر است", "时区无效": "منطقه زمانی نامعتبر است",
		},
		"vi": {
			"请求无效": "Yêu cầu không hợp lệ", "账号或密码错误": "Sai tên người dùng hoặc mật khẩu", "设置已保存": "Đã lưu cài đặt", "配置文件已生成": "Đã tạo tệp cấu hình", "入口已保存": "Đã lưu inbound", "入口已删除": "Đã xóa inbound", "连接已关闭": "Đã đóng kết nối", "核心状态已更新": "Đã cập nhật trạng thái lõi", "流量统计已重置": "Đã đặt lại thống kê lưu lượng", "界面语言无效": "Ngôn ngữ giao diện không hợp lệ", "入口不存在": "Không tìm thấy inbound", "缺少入口 ID": "Thiếu ID inbound", "入口名称不能为空": "Tên inbound không được để trống", "入口类型不能为空": "Loại inbound không được để trống", "密码不能为空": "Mật khẩu không được để trống", "客户端已保存": "Đã lưu client", "客户端已删除": "Đã xóa client", "Mihomo 配置测试通过": "Kiểm tra cấu hình Mihomo thành công", "面板正在重启": "Bảng điều khiển đang khởi động lại", "订阅路径无效": "Đường dẫn đăng ký không hợp lệ", "Clash 路径无效": "Đường dẫn Clash không hợp lệ", "运行模式无效": "Chế độ chạy không hợp lệ", "时区无效": "Múi giờ không hợp lệ",
		},
		"es": {
			"请求无效": "Solicitud no válida", "账号或密码错误": "Usuario o contraseña incorrectos", "设置已保存": "Configuración guardada", "配置文件已生成": "Configuración generada", "入口已保存": "Entrada guardada", "入口已删除": "Entrada eliminada", "连接已关闭": "Conexión cerrada", "核心状态已更新": "Estado del núcleo actualizado", "流量统计已重置": "Estadísticas de tráfico restablecidas", "界面语言无效": "Idioma de interfaz no válido", "入口不存在": "Entrada no encontrada", "缺少入口 ID": "Falta el ID de entrada", "入口名称不能为空": "El nombre de la entrada no puede estar vacío", "入口类型不能为空": "El tipo de entrada no puede estar vacío", "密码不能为空": "La contraseña no puede estar vacía", "客户端已保存": "Cliente guardado", "客户端已删除": "Cliente eliminado", "Mihomo 配置测试通过": "La configuración de Mihomo es válida", "面板正在重启": "El panel se está reiniciando", "订阅路径无效": "Ruta de suscripción no válida", "Clash 路径无效": "Ruta de Clash no válida", "运行模式无效": "Modo de ejecución no válido", "时区无效": "Zona horaria no válida",
		},
		"ja": {
			"请求无效": "無効なリクエスト", "账号或密码错误": "ユーザー名またはパスワードが正しくありません", "设置已保存": "設定を保存しました", "配置文件已生成": "設定ファイルが生成されました", "入口已保存": "インバウンドを保存しました", "入口已删除": "インバウンドを削除しました", "连接已关闭": "接続を閉じました", "核心状态已更新": "コア状態を更新しました", "流量统计已重置": "トラフィック統計をリセットしました", "界面语言无效": "無効なインターフェース言語", "入口不存在": "インバウンドが存在しません", "缺少入口 ID": "インバウンドIDが不足しています", "入口名称不能为空": "インバウンド名は空にできません", "入口类型不能为空": "インバウンドタイプは空にできません", "密码不能为空": "パスワードは空にできません", "客户端已保存": "クライアントを保存しました", "客户端已删除": "クライアントを削除しました", "Mihomo 配置测试通过": "Mihomo設定テストに合格しました", "面板正在重启": "パネルを再起動しています", "订阅路径无效": "無効なサブスクリプションパス", "Clash 路径无效": "無効なClashパス", "运行模式无效": "無効な実行モード", "时区无效": "無効なタイムゾーン",
		},
		"uk": {
			"请求无效": "Недійсний запит", "账号或密码错误": "Невірне ім'я користувача або пароль", "设置已保存": "Налаштування збережено", "配置文件已生成": "Конфігурацію створено", "入口已保存": "Вхід збережено", "入口已删除": "Вхід видалено", "连接已关闭": "З'єднання закрито", "核心状态已更新": "Стан ядра оновлено", "流量统计已重置": "Статистику трафіку скинуто", "界面语言无效": "Недійсна мова інтерфейсу", "入口不存在": "Вхід не знайдено", "缺少入口 ID": "Відсутній ID входу", "入口名称不能为空": "Назва входу не може бути порожньою", "入口类型不能为空": "Тип входу не може бути порожнім", "密码不能为空": "Пароль не може бути порожнім", "客户端已保存": "Клієнта збережено", "客户端已删除": "Клієнта видалено", "Mihomo 配置测试通过": "Перевірка конфігурації Mihomo успішна", "面板正在重启": "Панель перезавантажується", "订阅路径无效": "Недійсний шлях підписки", "Clash 路径无效": "Недійсний шлях Clash", "运行模式无效": "Недійсний режим роботи", "时区无效": "Недійсний часовий пояс",
		},
		"pt-BR": {
			"请求无效": "Solicitação inválida", "账号或密码错误": "Nome de usuário ou senha incorretos", "设置已保存": "Configurações salvas", "配置文件已生成": "Arquivo de configuração gerado", "入口已保存": "Entrada salva", "入口已删除": "Entrada excluída", "连接已关闭": "Conexão fechada", "核心状态已更新": "Status do núcleo atualizado", "流量统计已重置": "Estatísticas de tráfego redefinidas", "界面语言无效": "Idioma da interface inválido", "入口不存在": "Entrada não encontrada", "缺少入口 ID": "ID da entrada ausente", "入口名称不能为空": "O nome da entrada não pode estar vazio", "入口类型不能为空": "O tipo de entrada não pode estar vazio", "密码不能为空": "A senha não pode estar vazia", "客户端已保存": "Cliente salvo", "客户端已删除": "Cliente excluído", "Mihomo 配置测试通过": "Teste de configuração do Mihomo aprovado", "面板正在重启": "O painel está reiniciando", "订阅路径无效": "Caminho de assinatura inválido", "Clash 路径无效": "Caminho do Clash inválido", "运行模式无效": "Modo de execução inválido", "时区无效": "Fuso horário inválido",
		},
	}
	for language, messages := range overrides {
		for source, translation := range messages {
			messagesLocalized[language][source] = translation
		}
	}
}
