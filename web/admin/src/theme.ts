import type { ThemeConfig } from 'antd'

// 管理台全局设计令牌（仅视觉呈现，不改变组件行为）。
// 品牌色 / 圆角 / 阴影集中在这里与 styles/global.css 维护。
export const appTheme: ThemeConfig = {
  token: {
    colorPrimary: '#4f56d8',
    colorInfo: '#4f56d8',
    colorLink: '#4f56d8',
    colorLinkHover: '#6a70e6',
    colorLinkActive: '#3b43b8',
    colorText: '#1d2433',
    colorTextSecondary: '#5c6579',
    colorTextTertiary: '#8a93a8',
    colorBgLayout: '#f3f5fa',
    colorBorder: '#dbe1ec',
    colorBorderSecondary: '#e8edf5',
    colorFillAlter: '#f6f8fc',
    borderRadius: 8,
    borderRadiusLG: 12,
    borderRadiusSM: 6,
    controlHeight: 34,
    fontSize: 14,
    fontFamily:
      "-apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, 'PingFang SC', 'Hiragino Sans GB', 'Microsoft YaHei', 'Noto Sans CJK SC', sans-serif",
    boxShadow:
      '0 6px 16px 0 rgba(15, 23, 42, 0.08), 0 3px 6px -4px rgba(15, 23, 42, 0.12), 0 9px 28px 8px rgba(15, 23, 42, 0.05)',
    boxShadowSecondary:
      '0 6px 16px 0 rgba(15, 23, 42, 0.08), 0 3px 6px -4px rgba(15, 23, 42, 0.12), 0 9px 28px 8px rgba(15, 23, 42, 0.05)',
    boxShadowTertiary:
      '0 1px 2px 0 rgba(15, 23, 42, 0.04), 0 1px 6px -1px rgba(15, 23, 42, 0.03), 0 2px 4px 0 rgba(15, 23, 42, 0.02)',
  },
  components: {
    Layout: {
      siderBg: '#0d1526',
    },
    Menu: {
      darkItemBg: 'transparent',
      darkSubMenuItemBg: 'transparent',
      darkItemColor: 'rgba(203, 213, 225, 0.78)',
      darkItemHoverBg: 'rgba(148, 163, 184, 0.12)',
      darkItemSelectedBg: '#4f56d8',
      darkItemSelectedColor: '#ffffff',
    },
    Table: {
      headerBg: '#f8fafc',
      headerColor: '#47536b',
      headerSplitColor: 'transparent',
      borderColor: '#eef1f7',
      rowHoverBg: '#f5f7ff',
    },
    Button: {
      controlHeight: 34,
      fontWeight: 500,
      primaryShadow: '0 4px 14px rgba(88, 92, 224, 0.22)',
    },
    Form: {
      labelColor: '#3a4358',
    },
    Breadcrumb: {
      itemColor: '#8a93a8',
      linkColor: '#6b7488',
      separatorColor: '#b3bac8',
    },
  },
}
