import {
  ApartmentOutlined,
  UserOutlined,
  RobotOutlined,
  MessageOutlined,
  FileTextOutlined,
  ThunderboltOutlined,
  ToolOutlined,
  DatabaseOutlined,
  AuditOutlined,
  DashboardOutlined,
  CloudServerOutlined,
} from '@ant-design/icons'
import type { ReactNode } from 'react'

export interface NavItem {
  key: string // 同时是路由 path(去掉前导 /)
  path: string
  label: string
  icon: ReactNode
  group: string
}

// 左侧菜单 + 主路由表共用的单一数据源。
export const NAV: NavItem[] = [
  { key: 'workspaces', path: '/workspaces', label: '工作空间', icon: <ApartmentOutlined />, group: '组织管理' },
  { key: 'users', path: '/users', label: '用户', icon: <UserOutlined />, group: '组织管理' },

  { key: 'models', path: '/models', label: '模型配置', icon: <CloudServerOutlined />, group: 'Agent 构建' },
  { key: 'prompts', path: '/prompts', label: 'Prompts', icon: <FileTextOutlined />, group: 'Agent 构建' },
  { key: 'tools', path: '/tools', label: 'Tools', icon: <ToolOutlined />, group: 'Agent 构建' },
  { key: 'kbs', path: '/kbs', label: '知识库', icon: <DatabaseOutlined />, group: 'Agent 构建' },
  { key: 'skills', path: '/skills', label: 'Skills', icon: <ThunderboltOutlined />, group: 'Agent 构建' },
  { key: 'agents', path: '/agents', label: 'Agents', icon: <RobotOutlined />, group: 'Agent 构建' },
	{ key: 'conversations', path: '/conversations', label: '会话', icon: <MessageOutlined />, group: '运行验证' },
  { key: 'audit', path: '/audit', label: '审计', icon: <AuditOutlined />, group: '安全治理' },

  { key: 'observability', path: '/observability', label: '可观测', icon: <DashboardOutlined />, group: '可观测' },
]
