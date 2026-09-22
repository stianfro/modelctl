import { defineConfig } from "vitepress";

export default defineConfig({
  title: "modelctl",
  description: "Install modelctl and manage OpenCode models and API tokens.",
  base: "/modelctl/",
  lang: "en-US",
  sitemap: { hostname: "https://stianfro.github.io/modelctl/" },
  themeConfig: {
    nav: [
      { text: "Get started", link: "/" },
      { text: "Commands", link: "/commands" },
      { text: "Releases", link: "https://github.com/stianfro/modelctl/releases" },
    ],
    socialLinks: [{ icon: "github", link: "https://github.com/stianfro/modelctl" }],
    footer: { message: "Released under the MIT License." },
  },
});
