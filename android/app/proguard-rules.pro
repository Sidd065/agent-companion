# kotlinx.serialization: keep generated serializers and companion lookups for our @Serializable models.
-keepattributes *Annotation*, InnerClasses, Signature, EnclosingMethod
-keepclassmembers @kotlinx.serialization.Serializable class io.github.sidd065.agentcompanion.** {
    static ** Companion;
    kotlinx.serialization.KSerializer serializer(...);
}
-keepclasseswithmembers class io.github.sidd065.agentcompanion.** {
    kotlinx.serialization.KSerializer serializer(...);
}
-keep,includedescriptorclasses class io.github.sidd065.agentcompanion.**$$serializer { *; }

# Readable stack traces.
-keepattributes SourceFile, LineNumberTable
-renamesourcefileattribute SourceFile
-dontwarn org.jetbrains.annotations.**
