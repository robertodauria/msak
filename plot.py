#!/usr/bin/env python3

import gzip
import json
import sys

import pylab

COLS = 3

def main():
    if len(sys.argv) < 2:
        print("Usage: {} <input.json>".format(sys.argv[0]))
        sys.exit(1)

    # Open plain JSON file
    print(len(sys.argv[1:])//(COLS+1)+1, COLS)
    figure, axis = pylab.subplots(len(sys.argv[1:])//(COLS+1)+1, COLS, squeeze=False)
    max_y = 0
    for file_idx, file in enumerate(sys.argv[1:]):
        with open(file, 'r') as f:
            data = json.load(f)
            # Read each array of data
            for idx, _ in enumerate(data):
                x, y = [0], [0]
                for point in data[idx]:
                    x.append(point["elapsed"])
                    y.append(point["rate"])
                    if point["rate"] > max_y:
                        max_y = point["rate"]
                print(file_idx//COLS, file_idx%COLS)
                axis[file_idx//COLS,file_idx%COLS].plot(x, y, "x-", label="stream {}".format(idx))
                axis[file_idx//COLS,file_idx%COLS].set_xlabel("time (s)")
                axis[file_idx//COLS,file_idx%COLS].set_ylabel("rate (B/s)")
                axis[file_idx//COLS,file_idx%COLS].legend()
    for x in axis:
        for y in x:
            y.set_ylim(0, max_y)
            y.grid(True)
    pylab.show()

if __name__ == "__main__":
    main()