from pptx import Presentation
prs = Presentation("Báo cáo tiến độ đồ án tốt nghiệp.pptx.pptx")
print(f"W: {prs.slide_width/914400:.2f}in, H: {prs.slide_height/914400:.2f}in")
