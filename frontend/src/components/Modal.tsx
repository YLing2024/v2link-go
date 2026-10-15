import { Modal as AntdModal } from 'antd'

// 通用弹窗：对 antd Modal 的薄封装，保留原有 props 语义（title / onClose / children / width）。
// Esc 与遮罩点击关闭沿用 antd 默认行为（keyboard / maskClosable 均为 true），与改造前自绘实现一致；
// 底部不渲染 antd 默认按钮（footer=null），动作按钮仍由各弹层自行排布，文案与顺序不变。
export default function Modal({
  title,
  onClose,
  children,
  width = 420,
}: {
  title: string
  onClose: () => void
  children: React.ReactNode
  width?: number
}) {
  return (
    <AntdModal
      title={title}
      open
      onCancel={onClose}
      footer={null}
      width={width}
      keyboard
      maskClosable
      destroyOnHidden
    >
      {children}
    </AntdModal>
  )
}
