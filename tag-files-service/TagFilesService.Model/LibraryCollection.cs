namespace TagFilesService.Model;

/*
 * Series?
 * How to return collections and items as search results?
 */

public class LibraryCollection
{
    public LibraryCollection(string name)
        : this(0u, name, [])
    {
    }

    public uint Id { get; private set; }

    public string Name { get; private set; }

    public List<Tag> Tags { get; private set; }

    public void Rename(string newName)
    {
        Name = newName;
    }

    private LibraryCollection(uint id, string name, List<Tag> tags)
    {
        Id = id;
        Name = name;
        Tags = tags;
    }
}