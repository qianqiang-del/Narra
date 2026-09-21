<script setup lang="ts">
import { ConfigProvider } from 'reka-ui'
import { RouterView } from 'vue-router'
import { Toaster } from 'vue-sonner'
import 'vue-sonner/style.css'
</script>

<template>
  <!--
    关掉 reka-ui 的「滚动条补偿」：本项目已经在 <html> 上用 scrollbar-gutter: stable
    常驻了滚动条槽位（见 globals.css），弹层锁滚动时槽位照样留着、内容一点不挪 ——
    再按 `innerWidth - documentElement.clientWidth` 补一段 padding-right 就成了多余的
    位移：开弹层时 body 内容区窄 15px（居中的内容左移 7.5px、右对齐的按钮左移 15px），
    关掉再移回来，肉眼就是"打开往左抖、关闭往右抖"。

    给 0 补偿即无位移；同时也不要去依赖"库自己算的那段宽度" —— 槽位常驻时不同浏览器
    对这个宽度差的读法并不一致，钉死为 0 才是不动的最稳做法。

    锁滚动本身不受影响：html 的 overflow 保持默认 visible，所以 body 上那句
    `overflow: hidden` 会传播到视口，背景是真的锁住的（这正是 globals.css 里
    html 不能写 `overflow-y: scroll` 的原因）。`scrollBody` 传对象时也只有 padding /
    margin 两个旋钮，不碰焦点陷阱 / pointer-events / Esc；dir / locale 保持默认值
    （ltr / en），与注入上下文的兜底默认值一致，行为不变。
  -->
  <ConfigProvider :scroll-body="{ padding: 0, margin: 0 }">
    <RouterView />
  </ConfigProvider>
  <Toaster position="top-center" rich-colors />
</template>
