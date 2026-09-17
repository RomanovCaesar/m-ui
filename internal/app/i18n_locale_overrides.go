package app

// Human-reviewed translations override machine-generated text for the most
// visible API responses. The generated catalog still supplies every remaining
// message and every parameterized error.
func init() {
	overrides := map[string]map[string]string{
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
	}
	for language, messages := range overrides {
		for source, translation := range messages {
			messagesLocalized[language][source] = translation
		}
	}
}
