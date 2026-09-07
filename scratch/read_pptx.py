import zipfile
import xml.etree.ElementTree as ET
import sys
import re

def extract_text(filepath):
    text_runs = []
    with zipfile.ZipFile(filepath) as z:
        slide_files = [f for f in z.namelist() if re.match(r'^ppt/slides/slide\d+\.xml$', f)]
        slide_files.sort(key=lambda x: int(re.search(r'\d+', x).group()))
        
        for filename in slide_files:
            with z.open(filename) as f:
                tree = ET.parse(f)
                root = tree.getroot()
                namespaces = {'a': 'http://schemas.openxmlformats.org/drawingml/2006/main'}
                texts = root.findall('.//a:t', namespaces)
                slide_text = [t.text for t in texts if t.text]
                if slide_text:
                    text_runs.append(f"--- Slide {re.search(r'\d+', filename).group()} ---\n" + "\n".join(slide_text))
    return "\n\n".join(text_runs)

if __name__ == '__main__':
    print(extract_text(sys.argv[1]))
