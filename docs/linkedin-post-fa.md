# پست لینکدین: چرا Gopad را ساختیم؟

تصویر پیشنهادی برای پست: [gopad-crdt-study-linkedin.jpg](media/gopad-crdt-study-linkedin.jpg)

## متن پیشنهادی

چرا یک ویرایشگر متن اشتراکی دیگر ساختیم؟

هدف اصلی از ساختن Gopad، ساختن یک جایگزین برای ابزارهای بزرگ و آماده نبود. می‌خواستیم CRDT را از حالت مقاله، نمودار و تعریف تئوری خارج کنیم و با پیاده‌سازی یک سیستم واقعی بفهمیم وقتی چند کاربر هم‌زمان روی یک متن کار می‌کنند، دقیقاً چه اتفاقی می‌افتد.

در Gopad دو replica می‌توانند بدون قفل‌کردن متن با هم کار کنند، تغییرات را از طریق WebSocket دریافت کنند و در نهایت به یک state یکسان برسند؛ حتی اگر operationها با ترتیب متفاوتی به هر replica برسند.

چیزهایی که در مسیر یاد گرفتیم فقط به «ادغام متن» محدود نبودند:

- هر کاراکتر باید identity پایدار داشته باشد؛ نه فقط یک index که با هر insert جابه‌جا شود.
- ترتیب‌دادن به insertهای هم‌زمان باید deterministic باشد.
- delete نباید هویت داده را نابود کند؛ tombstone برای convergence و replay مهم است.
- reconnect، snapshot و operation log بخش جدایی‌ناپذیر یک سیستم collaborative هستند.
- cursor و presence هم باید بخشی از تجربه‌ی realtime باشند، اما با state متن قاطی نشوند.
- برای اطمینان از رفتار سیستم، property test، fuzz test و race test به اندازه‌ی خود implementation مهم‌اند.

برای اینکه این مفاهیم را ملموس‌تر ببینیم، history scrubber را هم اضافه کردیم. حالا می‌شود در operation log به عقب رفت و stateهای قبلی متن را دید؛ یعنی فقط نتیجه‌ی نهایی را نداریم، مسیر رسیدن به آن هم قابل مشاهده است.

پیاده‌سازی Gopad با Go، WebSocket، SQLite، JavaScript ساده و یک CRDT از خانواده‌ی RGA انجام شده است. رابط کاربری هم عمداً حال‌وهوای terminal-noir دارد: پس‌زمینه‌ی تیره، سیگنال‌های teal برای همکاری زنده و amber برای مشاهده‌ی تاریخچه.

این پروژه برای ما یک آزمایشگاه کوچک برای یادگیری distributed systems است؛ جایی که می‌توانیم درباره‌ی ordering، causal relationships، persistence، reconnect و convergence با کد واقعی فکر کنیم، نه فقط با مثال‌های روی کاغذ.

Gopad متن‌باز است و کد، مستندات، benchmarkها و تست‌های آن در GitHub قرار دارند:

🔗 https://github.com/SlmnFz/gopad

اگر به CRDT، collaborative editing یا distributed systems علاقه دارید، خوشحال می‌شوم تجربه و نقدتان را بشنوم.

#Go #CRDT #DistributedSystems #WebSockets #OpenSource #SoftwareEngineering

---

تصویر thumbnail برای بخش Projects لینکدین: [gopad-linkedin-thumbnail.jpg](media/gopad-linkedin-thumbnail.jpg)
