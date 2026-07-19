-keep class com.jcraft.jsch.** { *; }
-keep class com.mwiede.jsch.** { *; }
-dontwarn com.jcraft.jsch.**
-dontwarn com.mwiede.jsch.**

# Google Play Services & ML Kit Code Scanner (GmsBarcodeScanning)
-keep class com.google.android.gms.common.api.internal.** { *; }
-keep class com.google.android.gms.internal.** { *; }
-keep class com.google.android.gms.dynamite.** { *; }
-keep class com.google.android.gms.tasks.** { *; }
-keep class com.google.mlkit.** { *; }
-keep class com.google.android.gms.vision.barcode.** { *; }
-keep class com.google.android.gms.vision.** { *; }
-keep class com.google.android.gms.common.** { *; }

-dontwarn com.google.android.gms.**
-dontwarn com.google.mlkit.**
