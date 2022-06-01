#!/usr/bin/bash
while read line; do
  fileName="java-$line.yaml"
  echo "Building $fileName ..../"
  echo "name: $fileName" > $fileName 
  echo "filetype: (jsp$|java$)" >> $fileName
  echo "target: line"  echo "name: $line" >> $fileName
  echo "filetype: (jsp$|java$)" >> $fileName
  echo "type: regex" >> $fileName
  echo "defaultpattern: ^.*import(\s*|=)%s.*$" >> $fileName
  echo "advice: Consult 3rd party documentation" >> $fileName
  echo "category: $line" >> $fileName
  echo "tags:" >> $fileName
  echo "- value: $line" >> $fileName
  echo "patterns:" >> $fileName
  echo "- value: $line" >> $fileName
done < third-party.txt
